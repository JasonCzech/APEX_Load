# Frontend user-workflow evaluation and E2E test design

Review date: 2026-09-06. Source revision: `87ce7626d`.

The frontend has a coherent workspace model and useful guards, but a new user can reach scenario creation before having the scripts needed to finish it. Prioritize one continuous first-run journey, recovery from incomplete setup, and preservation of project/run identity across screens. Existing per-step browser coverage is substantial; it does not prove that a user can complete the whole journey without fixture setup between steps.

Implementation follow-up: [automated coverage and normal test cycle](WORKFLOW_REGRESSION_SUITE.md). The observations below describe the original audit, before that implementation. [Annotated Figma review board](https://www.figma.com/design/9tjL68AABdh0cOtSDvexeT).

## Evidence and scope

Reviewed React routing, existing Playwright specs, their fixtures, and the Tier-0 assertion matrix. Manually exercised the current Vite frontend with a freshly built controller, isolated temporary SQLite database, dev-admin identity, and the in-app browser. Screenshots are 932 × 1096 pixels in dark theme. No production records were touched and no load was launched.

The browser evaluation covers first-use navigation, scenario validation, script-selection recovery, and draft protection. Import execution, authenticated roles, scheduling, live runs, reports, downloads, native desktop behavior, and other viewport sizes are proposed test coverage, not newly verified results. Existing specs were inspected, not run; historical `GREEN` labels are not a fresh pass report. No application code or executable tests were changed.

## Observed journey

| Step | User action | Observed outcome | Health |
|---|---|---|---|
| 1 | Open the empty dashboard | Live connection, explicit empty activity, persistent workspace scope, New Test navigation | Usable; first-task guidance is visually subordinate to zero-value metrics |
| 2 | Choose New Test | Explains missing scenarios, offers Open Scenario Builder, disables launch | Good prerequisite handling; observed in accessibility tree, no separate screenshot |
| 3 | Open Scenario Builder | Five model choices, six steps, prominent Save Scenario before any input | Usable for experienced users; primary-action hierarchy can encourage premature saving |
| 4 | Save blank scenario, then supply a name and save again | Jumps to Review, first asks for a name, then requires a selected script; draft name survives | Correct rejection; recovery requires finding a different wizard step |
| 5 | Choose Scripts, then Add Script | Focus moves to an empty library search; no create/import action in this empty picker | First-use friction: the dependency cannot be supplied within this flow |
| 6 | Choose sidebar Scripts, then Keep editing in Unsaved Changes | Dialog receives focus; closing it restores navigation focus and preserves the draft name | Good draft protection; save/discard branches not exercised |

### 1. Empty dashboard

![Empty dashboard](frontend-review-2026-09-06/01-dashboard.png)

### 3. Scenario entry

![Scenario start](frontend-review-2026-09-06/02-scenario-start.png)

### 4. Validation recovery

![Missing script validation](frontend-review-2026-09-06/03-validation.png)

### 5. Empty script selection

![Empty script picker](frontend-review-2026-09-06/04-empty-script-picker.png)

## Findings and recommendations

| ID | Priority | Evidence | User impact and recommendation | Tests |
|---|---|---|---|---|
| F1 | P1 | Steps 2, 5, 6 | New Test leads to a builder whose empty script picker only offers search. Leaving to create a script triggers the dirty-draft guard. Offer Create/Import Script with an explicit return path that preserves the draft, or check this prerequisite before starting. This is friction, not proof that scripts cannot be created elsewhere. | U01, U03, U07 |
| F2 | P2 | Step 3 | Save Scenario visually outranks Next at the first step. Its validation then jumps to Review. Make the next required setup action primary and give validation an actionable link to the missing field/step. | U03, U08 |
| F3 | P2 | Step 4 | Review says “SCRIPTS 1 group” while the group has an unassigned script; default Run Settings/Data show completion ticks without visits. Distinguish valid defaults and configured groups from selected scripts so readiness is easier to judge. | U08, U09 |
| F4 | P2 | Screenshot 03 | At the captured width, the bottom action row overlaps the visible review table/help area. Verify all content can be scrolled fully clear of sticky controls; reserve footer space and test focus scrolling. This capture does not establish permanent inaccessibility. | U20 |
| F5 | P2 | Screenshot 01 | The empty release snapshot occupies most of the initial view; first-run activity guidance is near the bottom. Prefer a short first-run checklist and compact empty metrics. | U01, U20 |

Accessibility strengths: controls expose names, Add Script moves focus to search, and the unsaved-changes dialog initially focuses Keep editing and restores focus on cancellation. The validation banner has `role="alert"` in `TestHub.tsx`; its announcement still needs assistive-technology verification. Muted purple/gray text on dark backgrounds is a contrast risk requiring measurement, not a confirmed contrast failure. Tab controls exposed as checkboxes and table/focus behavior need keyboard and screen-reader evaluation. No WCAG compliance claim is made.

## Test environment and acceptance rules

- Use a throwaway controller/database/archive directory with an explicitly verified auth profile. Use dev-admin for functional isolation, local-auth for sign-in, and real role fixtures for RBAC; the DEV VIEW User toggle is not an authorization test.
- Use two projects with deliberately similar script/scenario/run names, distinct applications/releases, and unique test prefixes. Include empty and populated workspaces.
- Use a deterministic HTTP test service on a dedicated local port with successful, slow, and failing endpoints. Maximum three VUsers and short bounded runs. Never target production or depend on public services. Use a real generator pool only for the pool-assignment case.
- Keep the existing tiny OpenAPI fixture as the import baseline. Add malformed input, unsupported-runtime import, expired inspection, long names, and a 400-action script. Use synthetic data and dummy secrets.
- Pin report-feature enabled/disabled variants, browser locale, timezone, and viewport. Proposed baseline: 1440×900; reproduce this review at 932×1096; additionally test 1280×720 and 200% zoom. Add smaller widths only if they are product-supported.
- Drive the action under test through the UI. API setup and independent persistence checks are useful, but must not replace the UI step being evaluated. U01 must create its own business objects through the UI and retain them throughout.
- Poll observable states with deadlines; do not use fixed sleeps or exact live TPS/latency values. After pause, allow in-flight work/aggregation to drain before testing stability. Reconcile final metrics after terminalization.
- Every case records screenshot/trace on failure, identity/scope, relevant response status, expected versus observed outcome, and cleanup result. Test-owned runs must be terminated in fixture teardown even after timeout; reuse `support/liveRun.ts` where appropriate.
- P0 = essential release gate; P1 = high-value regression; P2 = broader usability/compatibility. These are proposed expectations, not claims that all cases currently pass.

## Proposed E2E cases

Each row specifies setup, user steps, and a measurable outcome. “Existing” means related inspected coverage exists; extensions still need implementation and execution.

| ID / priority | User goal and setup | User steps | Expected outcome | Coverage relationship |
|---|---|---|---|---|
| U01 / P0 | First successful test; fresh local-auth workspace and controlled HTTP target | Sign in → create Domain/Project/Application/Release → import OpenAPI → validate/convert/save → edit and save script → create scenario → preflight/estimate → start → watch → stop → find Results → download evidence | One continuous set of object IDs and scope; no hidden API-created business objects between stages; exactly one run; advancing samples; graceful COMPLETE; correct report/archive and nonempty download | Full arc explicitly MISSING in current matrix; compose existing per-step workflows |
| U02 / P0 | Sign in and preserve authorized context; local-auth plus viewer/operator/admin fixtures | Enter wrong credentials, correct them, open permitted views, sign out, use Back and a protected deep link; repeat with viewer | Failed login stays outside workspace; successful identity is correct; sign-out prevents protected data reuse; viewer cannot launch or mutate via UI/deep link; denied requests leave no changes | Extend local-auth/auth specs; role behavior needs real fixtures |
| U03 / P1 | Recover when no scripts/scenarios exist; empty project | New Test → Builder → name draft → Scripts → Add Script → attempt to obtain first script and return | Missing prerequisite is clear; proposed create/import return path retains name/settings; selected script belongs to this project; no silent discard or duplicate scenario | New; F1/F2; current empty picker lacks inline recovery |
| U04 / P0 | Keep work in the intended project; two similar workspaces | Select project A, open script/scenario, switch to B; test Back/Forward, reload and copied deep link; create an asset after switching | Header, URL, lists and persisted ownership agree; no A data or stale metrics under B; unauthorized asset links show access recovery rather than content | Extend workspace/routes specs |
| U05 / P0 | Import safely and commit once; valid OpenAPI | Upload → Validate Only → Convert → inspect generated action/method/target → Save → search/reopen library entry | Validation/conversion create no library item; explicit save creates exactly one correctly scoped script; reopened content matches review | Existing import baseline; add duplicate-submit/reload checks |
| U06 / P1 | Recover from failed/expired import; malformed and valid fixtures | Submit malformed input → correct file; separately expire inspection or return a controlled commit failure → retry | Actionable message; no partial library entry; safe retry/revalidation; corrected input remains available; no secret values in UI errors | New fault/recovery variants; use explicit server fault fixture |
| U07 / P0 | Protect unsaved work; dirty script and scenario | Attempt sidebar navigation, scope switch and browser Back; separately choose Keep editing, Save changes and Discard; inject save failure | Keep retains all fields; successful Save persists once then reaches intended destination; Discard leaves stored version unchanged; failed Save keeps user/draft in place | Existing scenario cancellation only; extend to all branches and editor |
| U08 / P0 | Fix invalid scenario; blank/manual draft | Save blank; add name but no script; select script then try invalid duration/VUsers/group configuration; correct and save | No invalid persistence; errors identify field/step and are announced; correction retains valid input; Review reflects selected scripts and actual totals; one valid save | Happy path exists; negative/recovery assertions new |
| U09 / P1 | Compose realistic workload; two scripts, data pool and distinct group roles | Configure groups, init scopes, duration, ramp, data; traverse every step backward/forward, Review, save, reopen | Group identities, role, data binding, duration units, VUsers and ramps survive; summary and stored model agree; model changes do not silently reset unrelated inputs | Extend scenario-builder spec |
| U10 / P0 | Launch exactly the reviewed load; valid scenario | Select release/name, apply run-only overrides, wait for preflight/estimate, change load again, recheck, double-click Start under latency | Stale preflight cannot authorize changed inputs; one run launches with final values; scenario defaults remain unchanged; UI reaches the created run | Extend run-launch; use request counts and persistent run ID |
| U11 / P1 | Recover from unavailable capacity; known unavailable/denied pool | Select placement, preflight, inspect failure, change to available pool and retry; reload configuration | No unintended auto-placement or launch; blocker names corrective action; chosen `pool_id` survives save/reload and generator assignment agrees | Matrix relies partly on lower-level tests; add UI round trip |
| U12 / P0 | Trust live monitoring; executing controlled run | Open live view, observe two samples, navigate to Command Center and back; interrupt/reconnect WebSocket | Progress is observable; disconnect/staleness is visible; reconnection resumes the same run without duplication or history from another project | Existing live metrics; reconnect and cross-view variants new |
| U13 / P0 | Pause/resume, scale and extend deliberately; running test | Open control confirmation and cancel; confirm pause; wait for in-flight drain; resume; scale 1→3; extend duration | Cancel sends no mutation; PAUSED plus stable completed-action counter after drain; resumed progress; visible target becomes 3; projected end moves consistently | Existing lifecycle baseline; add counter stability and reconnect checks |
| U14 / P0 | Distinguish stop from abort; two independent runs | Gracefully Stop run A; force Abort run B; reopen each from Results | A reaches COMPLETE, B ABORTED; no further target traffic after shutdown deadline; final status agrees across views; unavailable evidence is clearly labeled | Existing exact-terminal tests; add cross-view/traffic checks |
| U15 / P1 | Schedule or hold without accidental execution; valid scenario and fixed timezone | Hold for manual start, verify idle then start; schedule future run, reload, cancel; try past/invalid time; repeat across DST boundary | Hold never starts automatically; due run starts once; canceled run never starts; invalid/ambiguous time handled explicitly; displayed time/zone matches persisted instant | New scheduling journey; requires controllable scheduler clock/fixture |
| U16 / P0 | Understand and retrieve results; completed and failed runs | Filter by name/release/status, open run, switch report lenses where enabled, download listed artifact, clear filters | Right run remains identifiable; shared headline values agree; empty filter has recovery; downloaded artifact belongs to same run and matches manifest checksum | Extend report-results/lifecycle; test feature OFF legacy archive path too |
| U17 / P1 | Recover when evidence is incomplete; terminal run with unavailable/corrupt artifact fixture | Open archive/report; verify integrity; attempt download; retry when artifact becomes available | No false “verified” or successful-download indication; useful status/retry; healthy artifacts still accessible; no blank report | New; controlled artifact fixtures only |
| U18 / P1 | Handle concurrent edits and expired identity; two sessions | Edit same asset in both, save A then stale B; separately expire B session before Save | Conflict never silently overwrites A; B draft recoverable; reauthentication returns to intended work without replaying a mutation automatically | New; requires session and conflict fixtures |
| U19 / P1 | Diagnose dependencies; disconnected generator/collector fixture | Open Monitoring/Fleet, inspect warning, filter scope, reconnect dependency, refresh | Actual unavailable/last-seen state is visible; no success-looking stale zero; recovery updates without losing scope; role-appropriate actions only | Route smoke does not prove this workflow |
| U20 / P1 | Complete setup using keyboard and limited space | At baseline/review viewport and 200% zoom, traverse selectors, wizard, picker, validation and modal using keyboard; scroll Review to end | Logical focus order, visible focus, no traps/obscured focused controls, fully reachable table/help/footer; errors announced; contrast measured in both themes | New; F4/F5; add automated accessibility scan plus manual screen-reader pass |
| U21 / P2 | Work with realistic library scale; 400 actions and large paginated catalog | Search last action, switch editor tabs, edit/save/reopen; sort/filter library, change page and clear search | Last item remains reachable; edits survive virtualization; counts/page reset correctly; proposed editor usability deadline ≤30s; no blank pane | Existing large-script bound; extend catalog interactions |
| U22 / P1 | Recover from a page/chunk/API failure; controlled 500 and lazy-chunk failure | Navigate each major destination, fail loading once, retry; separately open missing/deleted object deep link | Correct page heading and landmark appear; meaningful error/retry or not-found recovery; shell alone cannot count as success; no crash loop | Strengthen tier0-routes, which currently checks visible body plus pageerror after 400ms |
| U23 / P1 | Know whether an imported script can run; governed/unsupported runtime fixture | Import and review warnings, acknowledge only where appropriate, save and attempt preflight | Authorable/importable does not imply executable; missing runner/policy or unsupported protocol gives explicit blocker; never silently substitutes runtime | Bruno coverage exists; broader runtime matrix new |
| U24 / P2 | Record a browser task; local recording agent disconnected then connected | Open recording flow, follow setup, connect test agent, record controlled site, stop, review, save and reopen script | Actionable disconnected state; session lifecycle visible; recording stops cleanly; reviewed actions persist in correct project; dummy secrets remain redacted | Separate integration lane with provisioned agent/browser; not required for HTTP smoke |

## Automation sequence and release evidence

First implement U01, U03, U07, U08 and U10: they cover the continuity gap, observed first-use friction, and the highest-cost data-loss/duplicate-launch risks. Strengthen route assertions alongside them: require a destination-specific heading/landmark and resolved loading state, not merely a visible body.

Keep independent per-step specs for diagnosis. Run the P0 critical path on PRs with deterministic fixtures, then add P1 recovery/RBAC checks as their harness support lands. Put timezone, multiple browser engines, agent integrations, large catalogs and wider viewport/theme coverage in a separate scheduled lane. Pin defaults explicitly; don't make the report-enabled path the only report/history acceptance test.

Proposed initial deadlines: ordinary local navigation/recovery 15s, large editor 30s, local launch/terminal transition 60s. Calibrate against reproducible baseline evidence before enforcing; these are test budgets, not claimed performance results. Use target-server counters for pause/stop semantics and run IDs for cross-view identity. Keep request-level inspection as corroboration of user-visible outcomes.

For every execution publish case ID, revision, profile, feature flags, browser/viewport, observed outcome, duration, trace/screenshot and cleanup. Mark Pass/Fail/Blocked/Not run explicitly. An unexecuted case or an API-only assertion must not turn the user journey green.

References: [existing journey](TIER0_JOURNEY.md), [assertion matrix](TIER0_ASSERTION_MATRIX.md), [browser specs](../../dashboard/e2e), [isolated harness](../../scripts/run-e2e.sh), [routing](../../dashboard/src/appRouting.ts), [generated API catalog](../api-endpoint-catalog.json).
