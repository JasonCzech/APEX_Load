# Completed telemetry, protocol & messaging slice reviews — Sep–Oct 2026

Status: Historical. Consolidated on 2026-10-29 from completed scoped review records under `docs/qa/reviews/`. Original dates, validation outcomes, failed intermediate checks, source hashes and qualification limits are preserved intact below. Untracked raw evidence remains local at the cited paths. Use the [active review index](../../qa/reviews/README.md) for current capstone records.

## Records
- [Engine snapshot synchronization review — 2026-10-03 (`feature-completion-eh-capacity-2026-10-03/engine-snapshot-review.md`)](#feature-completion-eh-capacity-2026-10-03-engine-snapshot-review)
- [Engine lifecycle progress review and verification (`feature-completion-eh-progress-2026-10-03/engine-review.md`)](#feature-completion-eh-progress-2026-10-03-engine-review)
- [Messaging management foundation review and verification (`feature-completion-management-2026-10-03/README.md`)](#feature-completion-management-2026-10-03)
- [Instructions (`feature-completion-management-2026-10-03/local-auth-disk-reserve-error-context.md`)](#feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context)
- [Shared messaging evidence review — 2026-10-03 (`feature-completion-messaging-2026-10-03/README.md`)](#feature-completion-messaging-2026-10-03)
- [Immutable telemetry policy receipt — 2026-10-05 (`feature-completion-telemetry-policy-2026-10-05/README.md`)](#feature-completion-telemetry-policy-2026-10-05)
- [gRPC diagnostic capture review (`protocol-diagnostics-2026-09-30/REVIEW.md`)](#protocol-diagnostics-2026-09-30)
- [Controlled protocol endpoint review — 2026-10-20 (`roadmap-controlled-endpoints-2026-10-20/README.md`)](#roadmap-controlled-endpoints-2026-10-20)
- [Roadmap implementation: coordinated starts across published-clock drift (`roadmap-coordinated-published-clock-2026-10-19/README.md`)](#roadmap-coordinated-published-clock-2026-10-19)
- [Roadmap implementation: durable controller cursors (`roadmap-durable-cursors-2026-10-19/README.md`)](#roadmap-durable-cursors-2026-10-19)
- [Roadmap implementation: Java HTTP body conversion (`roadmap-java-body-conversion-2026-10-19/README.md`)](#roadmap-java-body-conversion-2026-10-19)
- [Java encoding admission and protocol regression inventory — 2026-10-19 (`roadmap-java-encoding-admission-2026-10-19/README.md`)](#roadmap-java-encoding-admission-2026-10-19)
- [Java opaque representation review — 2026-10-20 (`roadmap-java-representations-2026-10-20/README.md`)](#roadmap-java-representations-2026-10-20)
- [Publication identity and render population — 2026-10-22 (`roadmap-render-population-2026-10-22/README.md`)](#roadmap-render-population-2026-10-22)
- [Roadmap implementation: source collection windows (`roadmap-source-windows-2026-10-19/README.md`)](#roadmap-source-windows-2026-10-19)
- [Run telemetry budget choice — 2026-10-23 (`roadmap-telemetry-budget-choice-2026-10-23/README.md`)](#roadmap-telemetry-budget-choice-2026-10-23)
- [Roadmap implementation: replay-safe telemetry coverage (`roadmap-telemetry-coverage-2026-10-19/README.md`)](#roadmap-telemetry-coverage-2026-10-19)
- [Atomic telemetry acceptance notifications — 2026-10-20 (`roadmap-telemetry-notification-runtime-2026-10-20/README.md`)](#roadmap-telemetry-notification-runtime-2026-10-20)
- [Telemetry notification outbox storage — 2026-10-20 (`roadmap-telemetry-notification-storage-2026-10-20/README.md`)](#roadmap-telemetry-notification-storage-2026-10-20)
- [In-run telemetry policy segments — 2026-10-23 (`roadmap-telemetry-policy-segments-2026-10-23/README.md`)](#roadmap-telemetry-policy-segments-2026-10-23)
- [Telemetry acceptance policy selection — 2026-10-22 (`roadmap-telemetry-policy-selection-2026-10-22/README.md`)](#roadmap-telemetry-policy-selection-2026-10-22)
- [Telemetry publication alignment — 2026-10-23 (`roadmap-telemetry-publication-alignment-2026-10-23/README.md`)](#roadmap-telemetry-publication-alignment-2026-10-23)
- [Telemetry text sanitization — 2026-10-22 (`roadmap-telemetry-sanitization-2026-10-22/README.md`)](#roadmap-telemetry-sanitization-2026-10-22)
- [Scenario telemetry budget choice — 2026-10-23 (`roadmap-telemetry-scenario-budget-2026-10-23/README.md`)](#roadmap-telemetry-scenario-budget-2026-10-23)
- [Scenario telemetry acceptance policy — 2026-10-22 (`roadmap-telemetry-scenario-policy-2026-10-22/README.md`)](#roadmap-telemetry-scenario-policy-2026-10-22)

<!-- Original record: docs/qa/reviews/feature-completion-eh-capacity-2026-10-03/engine-snapshot-review.md; local evidence kept outside git -->
<a id="feature-completion-eh-capacity-2026-10-03-engine-snapshot-review"></a>

## Engine snapshot synchronization review — 2026-10-03


The first repository attempt failed positive snapshot assertions that assumed asynchronous aggregation and resource sampling would complete within fixed 150 ms and 90 ms sleeps. The tests now wait up to five seconds for a published snapshot. The raw-action fixture queues all ten samples before Start, ensuring they drain into one aggregation window even when the test goroutine is delayed.

All transaction/error/action counts, active VUser gauges, resource presence and timestamp assertions remain. The negative idle-window case retains its original 90 ms observation period and absence assertion. No production code, collection interval or production deadline changed.

The protocol owner independently reviewed the test-only diff and found no additional issues. Focused verification passed with `-count=1`: default engine fixtures in 0.604 seconds and race fixtures in 1.765 seconds, without a race warning. Retained output: default (`engine-snapshot-default.txt`), race (`engine-snapshot-race.txt`).

```sh
GOCACHE=/private/tmp/apexload-feature-completion-go-cache GOMAXPROCS=2 GOMEMLIMIT=2GiB \
  go test -p 1 -count=1 -timeout 5m ./pkg/engine \
  -run '^(TestMetricsCollector_AggregateProduces(Snapshot|IdleSnapshot)|TestMetricsCollector_EmptyWindowSnapshotEmission)$'
```

The same command with `-race` passed. These focused checks supplement the final repository gate; they do not replace it.

The first targeted API race attempt failed the existing HTTP preparation supervision RPC deadline and cleanup wait (49.690 seconds); no data-race warning appeared. Its original output (`api-resource-race-r0-first-attempt.txt`) is preserved as a failed attempt, not acceptance. The serialized current-source race passed in 20.828 seconds with the placement correction and unchanged fixture/production deadlines: final output (`api-resource-race-r0-final.txt`). The earlier deadline failure did not reproduce; the evidence does not establish its underlying scheduling cause.

<!-- Original record: docs/qa/reviews/feature-completion-eh-progress-2026-10-03/engine-review.md; local evidence kept outside git -->
<a id="feature-completion-eh-progress-2026-10-03-engine-review"></a>

## Engine lifecycle progress review and verification


This slice publishes bounded local Event Hubs processor lifecycle evidence separately from action results. It does not add transaction or action observations. The engine injects the actual group identity, clears untrusted origins, preserves per-row source capture timestamps, and replaces cumulative evidence by source identity and revision. Active reporters remain registered through pending cleanup; only completed protocol teardown plus a `released` processor snapshot retires a reference. Final collector sampling records explicit incomplete coverage for unresolved cleanup and releases its remaining provider references.

Independent review and the final self-review corrected a phase-eligibility issue: at-once main VUsers register only when their main phase consumes; shared init and teardown protocols use their own phase. This prevents false reporter overflow in large producer main phases with a single lifecycle consumer. Independent review also corrected a final-sampling registration race: the collector now closes registration before capturing its final inventory. A deterministic blocked-capture regression verifies that late registrations cannot disappear from an accepted inventory. A final malformed-adapter review also contained panicking metadata and typed-nil adapters before registration can escape established lifecycle panic guards; bounded malformed coverage remains visible without retaining an unsafe provider. Source-owned history eviction and provisional generation replacement preserve cumulative loss coverage across replays. Shared merger priority was separately corrected so current ownership remains visible before bounded historical rows.

The meaningful regressions cover no-action idle windows, real engine pauses and scale-to-zero, final publication before a first tick, partial/malformed/panicking sources, teardown drain/release/quarantine, revision and capture replay ordering, deep-clone isolation, bounded provider references and retained bytes, producer-only and at-once phase exclusion, expected-source failure, and direct/closed/open/at-once execution. Zero-VUser at-once uses the existing explicit `AllowZeroVUsers` contract.

<a id="feature-completion-eh-progress-2026-10-03-engine-review--gates"></a>
### Gates

All commands used the dedicated cache `/private/tmp/apexload-feature-completion-go-cache`, `GOMAXPROCS=2`, `GOMEMLIMIT=4GiB`, and `-p 1`.

- Focused `TestMessagingProgress*`: passed, 3.786 seconds, exit 0; log (`engine-progress-default.txt`).
- Targeted progress/resource/idle/final-publication race tests: passed, 6.811 seconds, exit 0; log (`engine-progress-race.txt`).
- Complete `pkg/engine` suite: passed, 36.785 seconds, exit 0; log (`engine-full.txt`).
- Final engine whitespace check: passed.
- Independent provider-agent engine review: clean after final registration, source identity, eviction, missing-source, group, phase eligibility, and malformed-adapter fixes.

This is controlled local lifecycle and propagation evidence. It is not live Azure qualification. The existing soft pause still gates between actions; cancelling and acknowledging background receive/tail quiescence belongs to the following strict-pause slice. No claim of full Event Hubs feature completion is made here.

<!-- Original record: docs/qa/reviews/feature-completion-management-2026-10-03/README.md; local evidence kept outside git -->
<a id="feature-completion-management-2026-10-03"></a>

## Messaging management foundation review and verification


Status: shared MSG-2 foundation implemented, reviewed and verified. This record does not qualify a provider or close the full feature-completion program.

The slice adds exact presence-aware management contracts, immutable source/scope/principal approvals, signed source poll cohorts, singleton physical ownership fences, independent collector scheduling and bounded durable spooling, atomic per-run receipt publication, historical artifact decoding, management archive sidecars, and bounded API/CLI/dashboard reads. Production observer registration remains empty until each genuine provider adapter is implemented in a later reviewed commit.

Independent review findings addressed or under regression verification:

- Preflight signing configuration and separate unissued admission from issued authority. A failed signer can cancel only the exact unissued lease; an issued poll retains physical charge.
- Resolve lost sample acknowledgements using signed terminal metadata and the accepted immutable core digest. An accepted poll cannot become a fabricated abandoned result.
- Bind actual credential usernames/key names to the approved principal, with one controller/collector validation contract. Pin source credentials using a domain-separated opaque keyed version; rotation requires a new source revision.
- Reject released/terminal nonce reuse before observer I/O; use an independent cadence timer, coalesce missed ticks and prune unreferenced scheduling state.
- Exclude non-management and currently unready immutable source configurations from fair admission without bypassing an existing collecting/draining lease.
- Reject legacy point-only publication for a management-source job.
- Rebuild archive coverage deterministically on manifest refresh, retaining incomplete typed coverage for reports.
- Reserve response-wrapper bytes inside the bounded page budget and align controller/collector destination aliases.
- Recheck live immutable cohort authority at grant issuance/fresh GET, and serialize PostgreSQL terminal-status updates against fresh publication with deterministic test-row locks.
- Enforce the collector's 32 assigned jobs before Start/Prepare, allocate required bindings first, retain explicit optional capacity coverage, and permit terminal slot reuse.
- Compact terminal journals to minimal cohort identities while preserving source/hash/core/replay receipts. Retain retry/audit journals for seven days, and advance a bounded durable keyset cursor across receipt-protected rows with cancellation-aware storage transactions.
- Preserve legacy host-job readiness when dormant management approvals have no configured executable observer.

The intended verification gate includes common/collector/storage/API/CLI tests, targeted race tests, actual PostgreSQL migration/concurrency tests, full Go tests, generated API/TypeScript freshness, documentation checks, dashboard typecheck/lint/units, and affected browser flows in both auth profiles. Final results and immutable source identifiers will be appended after these gates pass.

Only the 10 GB import benchmark was waived. Protected Azure/broker qualification and native platform acceptance remain pending when their actual resources are unavailable. Local or mocked management observations do not promote Kafka, Service Bus or Solace support.

Verification completed before final integrated gates:

- Shared analysis contracts: full package normal test 0.347 seconds; targeted management/envelope race test 1.556 seconds.
- API/CLI initial focus: 1.942/0.793 seconds, before final lifecycle, generated-contract and retention regressions.
- Dashboard focused regression tests: 10 passed. Full dashboard unit suite: 458 files, 4,769 tests passed in 103.58 seconds.
- Dashboard lint and documentation consistency checks passed. Generated-contract refresh, final storage/API tests, actual PostgreSQL qualification, full Go and both browser profiles remain pending and must not be inferred from these preliminary gates.

Additional gate findings and final backend results:

- Actual PostgreSQL found the new poll-table foreign-key declaration shared a line with its ID. The existing restoration parser assigned that reference to the wrong column. Separate declarations and explicit project-FK/no-poll-ID parity regressions fixed the issue; a fresh disposable PostgreSQL 16 run passed.
- The full collector suite found an older OTLP restart fixture bypassed the constructor and therefore omitted the combined spool budget. Both fixture agents now use the actual constructor without weakening restart/overlapping-run assertions; independent review found no further issue.
- Final storage normal regressions passed in 7.964 seconds; targeted races in 65.280 seconds; actual PostgreSQL 16 migration/concurrency/retention and existing analysis workflows in 20.942 seconds. The disposable container and credential file were removed. Release 28 fingerprint: `8302b2875bbd64d91edac96ab4c39b6adf66bbfb288f60fbef5428718a1d4911`.

- Final deletion review found the filesystem artifact writer ignored cancellation. Entry/setup checks and a check after actual write/sync/close now prevent an expired writer from publishing; a real project deletion/outbox-drain regression covers cancellation after cleanup admission. Its first review also corrected a test-only mutex copy to use a fresh minimal server.
- Final artifact review found PostgreSQL TIMESTAMPTZ truncates receive timestamps to microseconds, which would invalidate exact immutable attribution checks. Controller receive clocks now use shared UTC microsecond precision; source capture clocks retain nanoseconds. Two actual PostgreSQL API regressions now verify precision after database reopen, signed publication, viewer reads and byte-identical archive rebuilds. Their combined targeted race gate passed in 49.510 seconds with no skipped PostgreSQL case; the disposable fixture and private credential file were removed.
- Final collector package normal tests passed in 8.390 seconds and focused races in 9.068 seconds. Authoritative API/TypeScript regeneration and final dashboard typecheck passed.

- The first full Go run passed every non-API package. API failures were confined to stale/new-route contract fixtures: the generated authorization matrix, three explicit access fingerprints and the middleware-public allowlist for in-handler collector-token authentication. These fixtures are updated with a dedicated token-vs-browser-role regression. Final metadata review also corrected the collector jobs/samples/poll catalog and Swagger authentication channels: exact route/handler matching and dedicated Bearer/API-key alternatives exclude inherited browser sessions. Generated assertions cover wrong handlers and unknown suffixes. Final regeneration and the expanded source/authorization/Swagger/generated-contract focus passed in 18.938 seconds. The full-suite repeat and browser cycle remain pending.

- The broad repeat hit three existing short timing waits and the API package's 30-minute timeout. All four affected/active cases passed on unchanged source in a focused run (8.488 seconds), so that run does not establish a product regression. Inspection did find an existing startup-test failure-path leak: teardown was registered only after the listener/readiness assertions. Teardown now registers before launch, joins startup through a reusable completion signal and closes SQLite after server cleanup. Independent review found no further issue; semantic assertions and their existing timing deadlines remain unchanged. Focused startup/cleanup checks passed in 2.132 seconds. The broad suite is repeating with a 60-minute package timeout for this host; application/test timing assertions remain unchanged. Final broad Go and browser results are still pending.

- A further full Go attempt was stopped after the generated QA dashboard freshness test failed; it is not a completed gate. `scripts/qa-generate-dashboard.py` regenerated the document (655 API contracts), and its check passed. The final full Go run follows the dashboard build.
- The initial dashboard cycle passed typecheck and lint, then reported two existing credential-review unit failures under host contention. That failed cycle was stopped. Both cases passed on unchanged source with one worker (2 selected tests, 3.30 seconds; 66 unselected cases in that file). The complete cycle is repeating with `VITEST_MAX_WORKERS=1`; no assertions or test deadlines were relaxed.
- A final independent static review checked admission/issuance, atomic publication/replay, ownership and authorization, historical evidence, pagination, scheduling/spooling/cleanup, retention and startup teardown. No additional actionable finding was found. Integrated Go and browser gates remain pending.

- The complete dashboard repeat passed typecheck, lint and all 458 unit files / 4,769 tests (1,198.43 seconds with one worker); the build passed. Dev-admin completed 193 browser passes and four pre-existing conditional skips. The local-auth run then passed 112 cases, conditionally skipped one and failed one existing Source Mapping case at its 30-second timeout. Every final body geometry assertion had completed in the retained trace; this is a failed full cycle, not a passing gate. The case repeats without changing assertions or deadlines. These workflows use bounded route fixtures for management presentation and do not qualify a provider.
- Real screenshot inspection found adjacent table columns touched for exact large values and long measurement labels. Scoped cell padding and a named keyboard-focusable horizontal scroll region fix this presentation issue. Independent review found no further issue; focused desktop geometry and narrow-screen keyboard verification in both auth profiles will follow the current full cycle. The retained before-spacing screenshots document the finding.

- The table-spacing correction passed typecheck, lint, 10 focused unit tests and an embedded dashboard build. Its full browser repeat passed dev-admin (193 cases, four conditional skips); local-auth is running. The wrapper does not forward selection arguments, so this is a full repeat rather than a focused gate.
- Narrow-screen screenshot review then identified a shared header flex-axis defect: the wider-layout 220px basis became vertical height below 520px. The mobile metadata and contribution controls now reset that basis. A repeated independent review found no further actionable issue. The regression checks compact header geometry and actual hit-testing at both ends of the keyboard-scrollable table, with table and viewport screenshots instead of a tall offscreen-element capture. Final execution is pending.

- Final mobile correction: typecheck and lint passed; four focused unit files / 12 tests passed in 4.16 seconds; the embedded dashboard build passed. Three selected real-controller browser workflows passed in dev-admin (14.0 seconds) and local-auth (54.7 seconds): exact management presentation/denied paging/reload, unchanged Source Mapping, and narrow Help. Root inspected both profiles' final desktop/table/viewport screenshots. Header geometry and table hit-testing passed; keyboard scrolling remained functional. The existing minified ScriptEditor chunk warning is unchanged from the prior accepted slice.
- Final full Go is running against the final source and dashboard build. The full browser repeat's local-auth profile is still in progress. Source hashes are captured; no final repository pass or slice commit is claimed yet.

- During final gates, free scratch space fell below 2 GiB while other host builds were active. Two abandoned temporary Go build directories were removed only after confirming the unique session-cache references, owner, unchanged files for over an hour and empty open-file inventory. This reclaimed 468,151,420 bytes (about 446 MiB). Live Go/frontend/Docker caches and deliverable outputs remain retained while the program needs them; this is not the final session cache cleanup.

- The full browser repeat completed dev-admin with 193 passes/four conditional skips; local-auth again passed 112, conditionally skipped one and failed the unchanged Source Mapping case. This occurrence timed out during its final viewport workflow and also exceeded context teardown; the trace archive was truncated and unavailable. It is retained as a failed gate, without attributing it solely to host timing or claiming a product fault.
- Independent review found the case combines four complete responsive workflows plus authentication/setup inside one 30-second budget. It is now four table-driven viewport cases, each opening at 1800px and resizing with the long mapping selected. Every prior geometry/selection/wrap/expansion/restoration/mappings assertion remains; explicit before/after `aria-current` checks add resize persistence. The narrow Parameter Data reveal remains in the 700px case. No deadline, retry or skip was changed. Repeated review found no additional issue; scoped lint and enumeration of all four cases passed. Both full browser profiles must pass after this refactor.

- The final complete Go repository gate passed with exit 0 against the reviewed source and embedded dashboard. The API package passed in 591.530 seconds; the committed package summary includes the raw log hash. Optional PostgreSQL cases in the default suite do not replace the actual PostgreSQL lanes recorded above. Both full browser profiles now repeat against the final mobile source and four independent viewport cases. No slice commit is claimed until they pass.

- The final broad browser run passed dev-admin (196 cases, four conditional skips) and all four split Source Mapping cases in local-auth. Local-auth completed 115 passes, one conditional skip and one failed U01 archive download. Its controller error reference `4f7e9c17fdcc8a46` proves the existing archive staging guard rejected a write while available disk was below the 1 GiB reserve. Independent review confirmed the guard, partial-file cleanup and admission release are intended and already tested; no new product defect was found. The failure and sanitized controller excerpt are retained. With disk recovered above 8 GiB, U01 repeats unchanged; no reserve, assertion, deadline, retry or skip is relaxed.

- Final unchanged local-auth U01 passed (22.4 seconds; one test / 24.4 seconds total, exit 0) with verified free disk above the archive reserve before and after. The final broad browser command remains recorded as exit 1; the aggregate gate consists of its 196 dev-admin passes / four conditional skips, 115 local-auth passes / one conditional skip, and the unchanged passing repeat of the sole reserve-blocked case. All four Source Mapping viewport cases passed in both profiles. Final management desktop/table/viewport screenshots were inspected in both profiles. No test or product limit was relaxed.

Completion: full Go, actual PostgreSQL storage/API races, generated contracts, documentation, dashboard typecheck/lint/full unit suite/build and final browser regressions are verified as detailed above. Independent repeated review found no remaining actionable finding in this slice. Production management observer registration remains empty; Kafka, Service Bus and Solace adapters and genuine-provider qualification follow in separate slices. Session caches remain retained for that dependent work.

<!-- Original record: docs/qa/reviews/feature-completion-management-2026-10-03/local-auth-disk-reserve-error-context.md; local evidence kept outside git -->
<a id="feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context"></a>

## Instructions


- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

<a id="feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context--test-info"></a>
## Test info

- Name: workflow-local-auth.spec.ts >> [U01] sign in, build workspace, import, edit, launch, stop and retrieve the same run
- Location: e2e/workflow-local-auth.spec.ts:7:1

<a id="feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context--error-details"></a>
## Error details

```
TimeoutError: page.waitForEvent: Timeout 15000ms exceeded while waiting for event "download"
=========================== logs ===========================
waiting for event "download"
============================================================
```

<a id="feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context--page-snapshot"></a>
## Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. arc-1791064638106 domain / arc-1791064638106 owner / arc-1791064638106 application / arc-1791064638106 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: arc-1791064638106 domain / arc-1791064638106 owner / arc-1791064638106 application / arc-1791064638106 1.0
        - generic [ref=e18]: ⌄
      - navigation "Primary navigation" [ref=e19]:
        - button "New Test" [ref=e21] [cursor=pointer]:
          - img [ref=e23]
          - generic [ref=e26]: New Test
        - generic [ref=e27]:
          - button "Dashboard" [ref=e29] [cursor=pointer]:
            - img [ref=e31]
            - generic [ref=e36]: Dashboard
          - button "Command Center" [ref=e38] [cursor=pointer]:
            - img [ref=e40]
            - generic [ref=e44]: Command Center
          - button "Results" [ref=e46] [cursor=pointer]:
            - img [ref=e48]
            - generic [ref=e49]: Results
          - button "Monitoring" [ref=e51] [cursor=pointer]:
            - img [ref=e53]
            - generic [ref=e55]: Monitoring
        - generic [ref=e56]:
          - button "Scripts" [ref=e58] [cursor=pointer]:
            - img [ref=e60]
            - generic [ref=e63]: Scripts
          - button "Scenarios" [ref=e65] [cursor=pointer]:
            - img [ref=e67]
            - generic [ref=e69]: Scenarios
        - generic [ref=e70]:
          - button "Repository" [ref=e72] [cursor=pointer]:
            - img [ref=e74]
            - generic [ref=e77]: Repository
          - button "Access" [ref=e79] [cursor=pointer]:
            - img [ref=e81]
            - generic [ref=e85]: Access
          - button "Settings" [ref=e87] [cursor=pointer]:
            - img [ref=e89]
            - generic [ref=e92]: Settings
        - button "Help & documentation" [ref=e95] [cursor=pointer]:
          - img [ref=e97]
          - generic [ref=e100]: Help & documentation
      - generic [ref=e101]:
        - generic [ref=e102]:
          - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e103]:
            - generic [ref=e104]: LS
            - generic [ref=e105]:
              - generic [ref=e106]: Local System Administrator
              - generic [ref=e107]: System Admin
              - button "Sign out" [ref=e108] [cursor=pointer]
          - generic [ref=e109]:
            - generic "Live" [ref=e110]:
              - generic [ref=e112]: Live
            - 'button "Build info: local · dev" [ref=e114] [cursor=pointer]':
              - generic [ref=e116]: Version Info
        - button "Collapse sidebar" [expanded] [ref=e118] [cursor=pointer]:
          - img [ref=e119]
    - main "Run Results" [ref=e121]:
      - status [ref=e122]: Run Results
      - generic "Dashboard controls" [ref=e123]:
        - heading "Run Results" [level=1] [ref=e125]
        - generic [ref=e126]:
          - link "Help for this page" [ref=e127] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e128]:
            - generic [ref=e129]: Browser · Server
          - combobox "Dashboard color theme" [ref=e132]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e133]:
        - generic [ref=e134]:
          - generic [ref=e135]:
            - paragraph [ref=e136]: Run results · LOC-RUN-20261003-030
            - heading "arc-1791064638106 run" [level=1] [ref=e137]
            - paragraph [ref=e138]:
              - generic [ref=e139]: Stopped by operator
              - text: · Configured 1 VUsers for 1m1s · Lifecycle elapsed 3s · Archived evidence elapsed 0m 3s
          - generic [ref=e140]:
            - button "Compare" [ref=e141] [cursor=pointer]
            - button "Export report" [expanded] [ref=e142] [cursor=pointer]
            - button "Test notes" [ref=e143] [cursor=pointer]
        - region "Report export options" [ref=e144]:
          - heading "Export report" [level=2] [ref=e145]
          - paragraph [ref=e146]: Choose an audience. Outputs cover the whole run, independent of the population selector below. Any release trend in an output uses the prior release run and names that reference.
          - tablist "Report audience lens" [ref=e147]:
            - tab "Executive" [selected] [ref=e148] [cursor=pointer]
            - tab "Engineer" [ref=e149] [cursor=pointer]
            - tab "Audit" [ref=e150] [cursor=pointer]
          - button "Preview / edit commentary" [ref=e151] [cursor=pointer]
          - generic [ref=e152]:
            - generic [ref=e154]:
              - heading "Export" [level=2] [ref=e155]
              - paragraph [ref=e156]: Choose a file for how your audience will review this executive report.
            - generic [ref=e157]:
              - button "Navigable HTML Browse sections and charts offline." [ref=e158] [cursor=pointer]:
                - strong [ref=e159]: Navigable HTML
                - generic [ref=e160]: Browse sections and charts offline.
              - button "Print-ready PDF Share a paged review copy." [ref=e161] [cursor=pointer]:
                - strong [ref=e162]: Print-ready PDF
                - generic [ref=e163]: Share a paged review copy.
              - button "Editable Word Add review notes and sign-off." [ref=e164] [cursor=pointer]:
                - strong [ref=e165]: Editable Word
                - generic [ref=e166]: Add review notes and sign-off.
              - button "Data CSV Use section and metric values in other tools." [ref=e167] [cursor=pointer]:
                - strong [ref=e168]: Data CSV
                - generic [ref=e169]: Use section and metric values in other tools.
              - button "Analysis tables (ZIP) Download row-based CSV tables and a data guide." [ref=e170] [cursor=pointer]:
                - strong [ref=e171]: Analysis tables (ZIP)
                - generic [ref=e172]: Download row-based CSV tables and a data guide.
          - group [ref=e173]:
            - generic "Publish to Power BI" [ref=e174] [cursor=pointer]
          - group [ref=e175]:
            - generic "Publish to Confluence" [ref=e176] [cursor=pointer]
        - region "Analysis context" [ref=e177]:
          - generic [ref=e178]:
            - text: Metric population
            - combobox "Metric population" [ref=e179]:
              - option "Requests / actions" [selected]
              - option "Named transactions"
              - option "Unclassified legacy records"
          - paragraph [ref=e180]: Requests / actions · 5671 samples · Whole captured run. Interval graphs show named transaction metrics. Resource curves show the load generators.
        - group [ref=e181]:
          - generic "Run timeline annotations" [ref=e182] [cursor=pointer]
        - tablist "Result sections" [ref=e183]:
          - tab "Summary" [ref=e184] [cursor=pointer]
          - tab "Performance" [ref=e185] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e186] [cursor=pointer]
          - tab "Diagnostics" [ref=e187] [cursor=pointer]
          - tab "Evidence" [selected] [ref=e188] [cursor=pointer]
          - tab "Execution" [ref=e189] [cursor=pointer]
        - tabpanel "Evidence" [ref=e190]:
          - region "Report unavailable" [ref=e191]:
            - heading "Report could not be loaded" [level=2] [ref=e192]
            - alert [ref=e193]: Controller reported a server error
            - paragraph [ref=e194]: Retry to load the archived findings. Execution details remain available.
            - link "Back to results" [ref=e195] [cursor=pointer]:
              - /url: "#/results?projectId=dd530a43-c7d4-4213-8faf-d3c236d656bf&applicationId=157501dc-c380-47af-bebd-5fc6220e7203&releaseId=b134dec0-01a5-455f-8702-f26b6369b30b"
            - button "Retry report" [ref=e196] [cursor=pointer]
            - button "View execution details" [ref=e197] [cursor=pointer]
          - generic [ref=e198]:
            - paragraph [ref=e199]: Controller reported a server error
            - generic [ref=e201]:
              - generic [ref=e202]:
                - heading "Completeness Score" [level=3] [ref=e203]
                - paragraph [ref=e204]: Archive coverage score for intervals, telemetry, writer health, and artifact availability.
              - generic [ref=e205]: 100% · complete
          - generic [ref=e206]:
            - heading "Metric populations" [level=2] [ref=e207]
            - generic "Metric populations" [ref=e208]:
              - generic [ref=e209]:
                - term [ref=e210]: Requests / actions
                - definition [ref=e211]: 5671 · 0 errors · p95 0.4 ms · 2149.088/s elapsed-run average
              - generic [ref=e212]:
                - term [ref=e213]: Named transactions
                - definition [ref=e214]: 5671 · 0 errors · p95 0.4 ms · 2149.088/s elapsed-run average
              - generic [ref=e215]:
                - term [ref=e216]: Unclassified legacy records
                - definition [ref=e217]: 0 records · no samples to evaluate
              - generic [ref=e218]:
                - term [ref=e219]: Archive records
                - definition [ref=e220]: "11342"
          - generic [ref=e221]:
            - paragraph [ref=e222]: Controller reported a server error
            - region "Archive metadata summary" [ref=e223]:
              - generic [ref=e224]:
                - generic [ref=e225]:
                  - generic [ref=e226]:
                    - heading "Telemetry Health" [level=3] [ref=e227]
                    - generic [ref=e228]: healthy
                  - generic [ref=e229]:
                    - generic [ref=e230]:
                      - term [ref=e231]: Samples
                      - definition [ref=e232]: "1"
                    - generic [ref=e233]:
                      - term [ref=e234]: Intervals
                      - definition [ref=e235]: 1 healthy · 0 degraded · 0 breached
                    - generic [ref=e236]:
                      - term [ref=e237]: Max Sample Age
                      - definition [ref=e238]: 53ms
                    - generic [ref=e239]:
                      - term [ref=e240]: Generator Lag
                      - definition [ref=e241]: n/a
                    - generic [ref=e242]:
                      - term [ref=e243]: Transport Lag
                      - definition [ref=e244]: n/a
                    - generic [ref=e245]:
                      - term [ref=e246]: Breach Window
                      - definition [ref=e247]: n/a
                - generic [ref=e248]:
                  - generic [ref=e249]:
                    - heading "Schema Compatibility" [level=3] [ref=e250]
                    - generic [ref=e251]: compatible
                  - generic [ref=e252]:
                    - generic [ref=e253]:
                      - term [ref=e254]: Report Schema
                      - definition [ref=e255]: apexload.archive_report.v1
                    - generic [ref=e256]:
                      - term [ref=e257]: Manifest
                      - definition [ref=e258]: apexload.archive.v4
                    - generic [ref=e259]:
                      - term [ref=e260]: Outputs
                      - definition [ref=e261]: "json: compatible, standalone html: compatible, transaction jsonl: compatible, resource jsonl: compatible, +7 more"
                    - generic [ref=e262]:
                      - term [ref=e263]: Issues
                      - definition [ref=e264]: None
                - generic [ref=e265]:
                  - generic [ref=e266]:
                    - heading "Evidence Tiers" [level=3] [ref=e267]
                    - generic [ref=e268]: 6 missing
                  - generic [ref=e269]:
                    - generic [ref=e270]:
                      - term [ref=e271]: Tiers
                      - definition [ref=e272]: "Summary: present · 11344, Intervals: present · 1, Sampled Diagnostics: missing · 0, Transaction Archive: present · 11342, Browser Artifacts: missing · 0, +5 more"
                    - generic [ref=e273]:
                      - term [ref=e274]: Present
                      - definition [ref=e275]: "4"
                    - generic [ref=e276]:
                      - term [ref=e277]: Missing
                      - definition [ref=e278]: "6"
                - generic [ref=e279]:
                  - generic [ref=e280]:
                    - heading "Run Provenance" [level=3] [ref=e281]
                    - generic [ref=e282]: captured
                  - generic [ref=e283]:
                    - generic [ref=e284]:
                      - term [ref=e285]: Execution Profile
                      - definition [ref=e286]: vuser_driven · 1 VUsers · 1m1s
                    - generic [ref=e287]:
                      - term [ref=e288]: Scope
                      - definition [ref=e289]: dd530a43-c7d4-4213-8faf-d3c236d656bf / b134dec0-01a5-455f-8702-f26b6369b30b / e2878e3f-8524-4d47-ada7-0a0609911d97
                    - generic [ref=e290]:
                      - term [ref=e291]: Runtime Flags
                      - definition [ref=e292]: Shakeout Disabled · Baseline Disabled
                    - generic [ref=e293]:
                      - term [ref=e294]: Browser Runtime
                      - definition [ref=e295]: n/a
                    - generic [ref=e296]:
                      - term [ref=e297]: Metrics Interval
                      - definition [ref=e298]: 1s
                    - generic [ref=e299]:
                      - term [ref=e300]: Pinned Baseline
                      - definition [ref=e301]: Not pinned
                    - generic [ref=e302]:
                      - term [ref=e303]: Scenario Groups
                      - definition [ref=e304]: Orders_1791064639559 · 1 VUsers
                    - generic [ref=e305]:
                      - term [ref=e306]: Controller Environment
                      - definition [ref=e307]: n/a
                    - generic [ref=e308]:
                      - term [ref=e309]: Generator Inventory
                      - definition [ref=e310]: n/a
                - generic [ref=e311]:
                  - generic [ref=e312]:
                    - heading "Change Attribution" [level=3] [ref=e313]
                    - generic [ref=e314]: captured
                  - generic [ref=e315]:
                    - generic [ref=e316]:
                      - term [ref=e317]: Scripts
                      - definition [ref=e318]: 5066221f-3d9b-4b1b-93de-df88e1fb65bd
                    - generic [ref=e319]:
                      - term [ref=e320]: Scenario
                      - definition [ref=e321]: e2878e3f-8524-4d47-ada7-0a0609911d97
                    - generic [ref=e322]:
                      - term [ref=e323]: Environment
                      - definition [ref=e324]: n/a
                    - generic [ref=e325]:
                      - term [ref=e326]: Generators
                      - definition [ref=e327]: n/a
                    - generic [ref=e328]:
                      - term [ref=e329]: Data
                      - definition [ref=e330]: 5066221f-3d9b-4b1b-93de-df88e1fb65bd:data
                    - generic [ref=e331]:
                      - term [ref=e332]: Runtime Overrides
                      - definition [ref=e333]: "No"
              - region "Archived monitoring capture" [ref=e334]:
                - heading "Monitoring capture evidence" [level=3] [ref=e335]
                - paragraph [ref=e336]: Samples are bounded, deduplicated, and redacted—not a complete log export. Empty evidence does not prove that no errors occurred.
                - paragraph [ref=e337]:
                  - strong [ref=e338]: "Elastic:"
                  - text: Capture not recorded for this run (disabled or older archive).
                - paragraph [ref=e339]:
                  - strong [ref=e340]: "Dynatrace:"
                  - text: Capture not recorded for this run (disabled or older archive).
          - group [ref=e341]:
            - generic "Raw evidence downloads" [ref=e342] [cursor=pointer]
            - button "Download evidence bundle" [ref=e343] [cursor=pointer]
            - generic [ref=e344]:
              - paragraph [ref=e345]: Controller reported a server error
              - generic [ref=e346]:
                - generic [ref=e347]:
                  - link "Download report data JSON" [ref=e348] [cursor=pointer]:
                    - /url: /api/v1/tests/903a11cc-d2d6-4f46-ab25-96b4c38fc970/archive/report?download=true
                  - generic [ref=e349]: JSONL
                  - button "Download Transactions JSONL" [active] [ref=e350] [cursor=pointer]
                  - button "Download Resources JSONL" [ref=e351] [cursor=pointer]
                  - button "Download Summaries JSONL" [ref=e352] [cursor=pointer]
                - generic [ref=e353]:
                  - generic [ref=e354]: Parquet
                  - button "Download Transactions Parquet" [ref=e355] [cursor=pointer]
                  - button "Download Resources Parquet" [ref=e356] [cursor=pointer]
                  - button "Download Summaries Parquet" [ref=e357] [cursor=pointer]
          - group [ref=e358]:
            - generic "Archived intervals" [ref=e359] [cursor=pointer]
  - generic [ref=e360]: "0"
```

<a id="feature-completion-management-2026-10-03-local-auth-disk-reserve-error-context--test-source"></a>
## Test source

```ts
  1   | import { readFile, stat } from 'node:fs/promises'
  2   | import { createHash } from 'node:crypto'
  3   | import { test, expect, login, createWorkspaceUI, importOpenAPI, buildScenarioUI, mutation, json, idOf, runStatus } from './support/workflow'
  4   |
  5   | test.beforeEach(() => { test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'local-auth profile') })
  6   |
  7   | test('[U01] sign in, build workspace, import, edit, launch, stop and retrieve the same run', async ({ page, target, trackRun }) => {
  8   |   test.setTimeout(240_000)
  9   |   await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  10  |   await login(page)
  11  |   const request = page.context().request
  12  |   const label = `arc-${Date.now()}`
  13  |   const workspace = await createWorkspaceUI(page, request, label)
  14  |   const scriptName = `Orders_${Date.now()}`
  15  |   const scriptId = await importOpenAPI(page, scriptName, target.url)
  16  |   const workbench = page.getByRole('region', { name: 'Author center workbench' })
  17  |   await expect(workbench.getByRole('tab', { name: 'Action editor' })).toHaveAttribute('aria-selected', 'true')
  18  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  19  |   await workbench.getByRole('textbox', { name: 'Base URL' }).fill(target.url + '/')
  20  |   const updated = mutation(page, 'PUT', `/api/v1/scripts/${scriptId}`)
  21  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  22  |   await json(await updated)
  23  |   await page.getByRole('button', { name: 'Close script editor' }).click()
  24  |   const scenarioId = await buildScenarioUI(page, scriptName, `${label} scenario`)
  25  |   await expect(page).toHaveURL(new RegExp(`scenarioId=${scenarioId}`))
  26  |   const runName = `${label} run`
  27  |   await page.getByLabel('Run Name', { exact: true }).fill(runName)
  28  |   await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  29  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeEnabled({ timeout: 30_000 })
  30  |   const created = mutation(page, 'POST', '/api/v1/tests')
  31  |   await page.getByRole('button', { name: 'Start Now' }).click()
  32  |   const run = await json(await created)
  33  |   const id = idOf(run); trackRun(id)
  34  |   expect(run.project_id).toBe(workspace.projectId)
  35  |   expect(run.name).toBe(runName)
  36  |   await expect(page).toHaveURL(new RegExp(`(?:test|command-center)/${id}`))
  37  |   await expect.poll(() => target.count(), { timeout: 60_000 }).toBeGreaterThan(0)
  38  |   const first = target.count()
  39  |   await expect.poll(() => target.count()).toBeGreaterThan(first)
  40  |   await expect.poll(() => runStatus(request, id)).toBe('RUNNING')
  41  |   await page.getByRole('button', { name: 'Stop', exact: true }).click()
  42  |   await page.getByRole('dialog', { name: 'Stop this test run?' }).getByRole('button', { name: 'Stop Test' }).click()
  43  |   await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  44  |   await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  45  |   await page.getByRole('button', { name: 'Results', exact: true }).first().click()
  46  |   await page.getByRole('searchbox', { name: 'Search results', exact: true }).fill(runName)
  47  |   const row = page.locator('tbody tr').filter({ hasText: runName })
  48  |   await expect(row).toBeVisible()
  49  |   await row.getByRole('button', { name: 'View results' }).click()
  50  |   await expect(page).toHaveURL(new RegExp(`#/report/${id}`))
  51  |   await expect(page.getByText(runName, { exact: false }).first()).toBeVisible()
  52  |   await page.getByRole('button', { name: 'Export report', exact: true }).click()
  53  |   const exportOptions = page.getByRole('region', { name: 'Report export options' })
  54  |   await expect(exportOptions.getByRole('button', { name: /Print-ready PDF/ })).toBeVisible()
  55  |   const analysisDownload = page.waitForEvent('download')
  56  |   await exportOptions.getByRole('button', { name: /Analysis tables/ }).click()
  57  |   const analysisBundle = await analysisDownload
  58  |   expect(analysisBundle.suggestedFilename()).toMatch(/-executive\.zip$/)
  59  |   const analysisPath = await analysisBundle.path()
  60  |   expect(analysisPath).not.toBeNull()
  61  |   expect((await stat(analysisPath!)).size).toBeGreaterThan(0)
  62  |   // Follow the report's evidence link, preserving the same run identity.
  63  |   await page.getByRole('tab', { name: 'Evidence', exact: true }).click()
  64  |   await page.getByText('Raw evidence downloads', { exact: true }).click()
> 65  |   const download = page.waitForEvent('download')
      |                         ^ TimeoutError: page.waitForEvent: Timeout 15000ms exceeded while waiting for event "download"
  66  |   await page.getByRole('button', { name: 'Download Transactions JSONL' }).first().click()
  67  |   const downloaded = await download
  68  |   const path = await downloaded.path(); expect(path).not.toBeNull()
  69  |   expect((await stat(path!)).size).toBeGreaterThan(0)
  70  |   const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`))
  71  |   const serverFile = await request.get(`/api/v1/tests/${id}/archive/files/${String(manifest.transactions_file).split('/').pop()}`)
  72  |   expect(serverFile.ok()).toBeTruthy()
  73  |   expect(createHash('sha256').update(await readFile(path!)).digest('hex')).toBe(createHash('sha256').update(await serverFile.body()).digest('hex'))
  74  | })
  75  |
  76  | test('[U02] viewer cannot launch and sign-out does not reopen a protected workspace', async ({ page }) => {
  77  |   await login(page, 'user')
  78  |   const auth = await json(await page.context().request.get('/api/v1/auth/me'))
  79  |   expect((auth.user as { subject: string }).subject).toBe('local:user')
  80  |   await page.goto('/#/new-test')
  81  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  82  |   const denied = await page.context().request.post('/api/v1/tests', { data: { name: 'viewer must not create', duration: '1m', groups: [] } })
  83  |   expect(denied.status()).toBe(403)
  84  |   await page.getByRole('button', { name: 'Sign out' }).click()
  85  |   await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  86  |   await page.goBack()
  87  |   await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  88  |   await expect(page.locator('nav.sidebar-nav')).toBeHidden()
  89  | })
  90  |
  91  | test('[U03] viewer personal settings persist without exposing administrative settings', async ({ page }) => {
  92  |   await login(page, 'user')
  93  |   const adminSettingsRequests: string[] = []
  94  |   page.on('request', request => { if (/\/api\/v1\/system\/settings(?:\?|$)/.test(request.url())) adminSettingsRequests.push(request.url()) })
  95  |   await page.locator('button.nav-item[aria-label="Settings"]').click()
  96  |   await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  97  |   await page.getByLabel('Dashboard Theme', { exact: true }).selectOption('light')
  98  |   await page.getByLabel('Motion', { exact: true }).selectOption('reduce')
  99  |   await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  100 |   await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  101 |   await page.getByRole('button', { name: 'Tables & editor', exact: true }).click()
  102 |   await page.getByLabel('Default rows per table', { exact: true }).selectOption('25')
  103 |   await page.getByLabel('Editor word wrap', { exact: true }).selectOption('off')
  104 |   await page.reload()
  105 |   await expect(page.getByLabel('Default rows per table', { exact: true })).toHaveValue('25')
  106 |   await expect(page.getByLabel('Editor word wrap', { exact: true })).toHaveValue('off')
  107 |   await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  108 |   await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  109 |   await page.getByRole('button', { name: 'Account', exact: true }).click()
  110 |   await expect(page.getByRole('region', { name: 'Your account' })).toContainText('Local Viewer')
  111 |   await page.goto('/#/settings?setting=authentication')
  112 |   await expect(page.getByLabel('Dashboard Theme', { exact: true })).toBeVisible()
  113 |   await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  114 |   expect(adminSettingsRequests).toEqual([])
  115 |   const denied = await page.context().request.put('/api/v1/system/settings', { data: {} })
  116 |   expect(denied.status()).toBe(403)
  117 | })
  118 |
```

<!-- Original record: docs/qa/reviews/feature-completion-messaging-2026-10-03/README.md; local evidence kept outside git -->
<a id="feature-completion-messaging-2026-10-03"></a>

## Shared messaging evidence review — 2026-10-03


Scope: optional messaging diagnostics v2, fixed bounded duration distributions, explicit operation/counter populations, Kafka/Event Hubs/JMS adapter observations and engine/RPC/archive propagation. This is the R1/MSG-1 evidence foundation, not complete broker qualification or the full messaging program.

Independent review/fix iterations resolved:

- Engine retention estimates omitted new histogram arrays, slice capacities and availability maps. Revised conservative accounting and queue-clone tests cover retained allocations.
- A claimed-v2 empty aggregate payload disappeared instead of marking missing coverage. Decoder/cohort regressions now preserve incompleteness for missing/unknown versions.
- Successful sends followed by assertion/extraction/reply failure lost accepted message/acknowledgement evidence. Internal adapter outcome markers retain genuine transport observations separately from action success.
- Actual failed sends omitted eligible v2 producer operations. Failed acknowledgements now retain unavailable reasons and scoped observed-zero/one failure counters; preflight/buffer admission and transaction flush populations remain separate.
- Observed JMS replies/deliveries followed by settlement errors lost received evidence. Explicit delivery markers preserve observations even with empty payload/metadata; they do not manufacture successful settlement.
- Go JSON decoding silently truncated oversized fixed histogram arrays and coerced null/omitted required numbers into zero. V2-specific bounded decoders reject malformed arrays, explicit null counts/statistics, absent counter counts and missing positive-observation statistics. Legacy distribution decoding is unchanged; wholly unavailable zero distributions retain compatibility.

The adapter/retention repeat review reported no additional actionable findings, including strengthened empty-body tests. The final strict-decoder repeat review also reported no additional actionable findings. Histograms merge bins, counts and availability rather than averaging percentiles. Legacy evidence stays readable and cannot manufacture v2 coverage; unsupported/malformed/oversized publication remains explicitly incomplete. Uncalibrated broker enqueue clocks remain unavailable in v2.

Validation:

- Full default protocol/Event Hubs/Kafka/JMS suites passed.
- Full real-adapter build profiles passed together: `eventhubs_real`, `kafka_real`, `jms_servicebus_real`, `jms_solace_real`. These runs compile the edited adapters and exercise deterministic/local fixtures. Live service acceptance was not run.
- Partial-delivery/empty-body regressions passed in default and combined tagged profiles.
- Focused engine/RPC bounded-retention, wire-version and mixed-peer regressions passed. Focused API messaging/native regressions passed.
- Final focused protocol/engine/RPC race suites passed after all decoder fixes (race log (`race-tests.txt`)); default repository-wide tests passed on frozen source with a dedicated temporary Go cache (repository log (`../feature-completion-catalog-2026-10-03/go-tests.txt`)).
- The final combined real-adapter build-profile repeat passed (adapter log (`adapter-tests.txt`)); no live provider qualification is inferred. Documentation and generated contract checks passed.
- The initial repository run encountered stale generated QA inventory and source changed during compilation. Inventory was regenerated and a frozen complete run passed; the interrupted decoder-follow-up run is not counted as a pass. A subsequent shared-cache run passed API tests but lost dependency cache files during remaining builds; the dedicated-cache full repeat passed and supersedes that environmental failure.

Remaining program gates include provider capability/admission matrices, management packs, user/report percentile workflows, qualified source clocks, helper semantics, restart/rebalance/provider certification and integrated acceptance. No provider support level is promoted by these component results.

<!-- Original record: docs/qa/reviews/feature-completion-telemetry-policy-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-telemetry-policy-2026-10-05"></a>

## Immutable telemetry policy receipt — 2026-10-05


Status: review/fix/review and final repository/dashboard qualification passed for this immutable policy-receipt slice. Ready for final staging/commit review. Full TEL-1–TEL-4 remain open. [Guide](../../guides/TELEMETRY_POLICY_RECEIPTS.md).

This slice freezes resolved legacy diagnostic budgets and actual controller cadence with complete initial configuration at atomic INITIALIZING admission. A private persistence projection prevents candidate exposure during SQL waits. The transaction retains existing project/deployment/execution/admission fences, decodes the write-once winner before commit, and returns an owned receipt only after success. Current and legacy winners are adopted before publishing or backend dispatch. Nil/default and zero/disabled stay distinct. Later settings do not reinterpret stored thresholds.

Dashboard admission verifies structural presence and exact WebCrypto semantic digest, with stale completion fencing. Actual Go/API boundary vectors cover exponent cutoffs, subnormals and negative zero. CLI projections are bounded, authorized, canonical, hash-checked and disclosure-safe. Live badges describe observed facts; archive classifications retain the explicit legacy algorithm. Absent summary/counts stay n/a. Typed generated contracts preserve required false/zero fields and actual nullable empty-history wire values.

<a id="feature-completion-telemetry-policy-2026-10-05--review-and-retained-failures"></a>
### Review and retained failures

Review/fix/review corrected asynchronous post-Start capture, whole-provenance copying on each publication, pending rollback placement, fractional cadence, cached losing receipts, concurrent uncommitted manifests, generated type/presence depth, null history, digest/content mismatch, zero capture time and invented healthy/zero displays. Test backends release and join before store/temp cleanup.

Prior-winner regression failed both current/legacy cases before correction (raw26aab0e3675f243530a14076d53a8d5eca2763593a70b5df010a42cc2d6fb918). Concurrent SQL barrier failed none/current/legacy before the private projection (raw29acb1ff3411dff1af3a56943914d53eb4b4e4ca6e43c2a7bede1c36b9f12f27). Both remain failed evidence. Earlier fixture-only settings changes bypassed real durable refresh; one-shot outages correctly retried; both fixtures were corrected. A valid null-history wire check initially failed only synthetic active-row shutdown, now exact owned-row retirement precedes real shutdown. The zero-time correction rejects the Go zero timestamp, including zero fractional digits, while retaining nonzero one-nanosecond year-one provenance. Earlier type/contract errors are retained.

<a id="feature-completion-telemetry-policy-2026-10-05--verified-checks"></a>
### Verified checks

Sibling evidence receipts retain command, UTC interval, duration, raw log/hash and source-before/after. Storage inventory was added when transaction scope expanded; earlier receipts do not retrospectively claim those inputs.

- Private admission, winner and concurrent-reader checks: three repeats passed in 65.364 seconds, raw SHA-256 2169545e9c6f4afd3f2da13826fff051a9c944f85f7365988972a1f9a4014b55. Actual SQLite close/reopen qualifies metadata, not full HA execution recovery.
- Actual SQLite/PostgreSQL16.15 ownership, corrupt-decode rollback and owned/wrong/expired execution lease checks: race two repeats passed in 32.500 seconds, raw SHA-256 409904f7632d6e722b3b5cd219a493410af7532702475e779c4804b9f6a8e94a. Owned PG schemas removed afterward.
- Typed/fresh generated contracts and metrics handlers passed in 62.255 seconds, raw SHA-256 fb75d6da613c45a449f313e3171f945159bafc4f7544dcb030a80655fb7b44f7, before the final private-candidate correction; the final combined API admission/lifecycle race repeat passed in 136.166 seconds, raw SHA-256 5d3c1edbbaf60a90591a011ed27d9d9aed93d114af8e876c1bae0663314fbc5d.
- Final-source CLI normal/race checks passed in 8.414/46.238 seconds; 138 affected frontend regressions also passed. Actual Go numeric digest vectors and Node WebCrypto agree, including negative zero and exponent boundaries.
- Full `go test ./...` passed in 1,353.763 seconds on final source and the frozen corrected bundle, raw SHA-256 714066a34b764cb18d97c18ff4e995660990bf1efd4c9b33ba33bb5460ea70e5. The complete dashboard cycle passed in 2,353.064 seconds, raw SHA-256 09408e6fb81fb5ff2a670c96e52e1fd5da3e849d57bd13af492c0ff14bbb78cb: typecheck, lint, all 514 unit files/5,738 tests, build, 243 dev-admin and 183 local-auth workflows. Five/one historical skips consist of profile-specific viewer/nonadmin cases and an absent private LoadRunner acceptance archive; both new telemetry cases ran in each profile. Skips do not qualify private-provider acceptance.
- Independent same-bundle stable visual proof passed in both profiles, 29.382/28.358 seconds, with two cases each and zero skips/retries. The real controlled HTTP case verifies admission, settings changes, live/archive/manifest receipt equality and complete visible policy cards after finite ancestor animations. Retained PNGs and geometry show full effective opacity, whole-card hit ownership, no overflow and actual Chromium 149.0.7827.55. A separate uniform CSS helper case verifies background layer/parent alpha composition against a PNG pixel decoded by existing bundled Pillow 12.3.0 and explicitly refuses unsupported backgrounds. Computed CSS gradient-endpoint reference contrast is not arbitrary pixel or general WCAG certification.

Final evidence index (`evidence/telemetry-consumer-final-evidence-index.json`) links exact source/bundle inventories, immutable receipts/raw logs, review and stable visual proof (`evidence/stable-visual-final-browser-proof-index.json`). The 20 consumer/chart source hashes and 247 embedded assets match before/after final proof. Earlier passed Go/browser gates remain historical: the first full Go run changed its bundle during the zero-time correction; the first full dashboard attempt was interrupted after its units before applying the canonical chart correction. Preliminary archive screenshots caught page fade; final captures wait for actual stable visibility. External helper refusals and the PATH Python/Pillow failure remain retained. No failed or interrupted attempt is represented as acceptance.

<a id="feature-completion-telemetry-policy-2026-10-05--remaining-scope"></a>
### Remaining scope

Source/counter epochs, observation windows, offset/uncertainty-qualified clocks, union coverage and gap-aware sustained intervals, exact/lower-bound increments, distributions and browser render observations, policy/workload comparison, separate telemetry verdicts, full report/export workflows and measured reference latency remain open. No new automatic stopping or CI outcome is added. Only 10 GB analytics acceptance was waived; mandatory 1 GiB, five-platform, restart/HA and remote-backend acceptance remain open elsewhere.

<!-- Original record: docs/qa/reviews/protocol-diagnostics-2026-09-30/REVIEW.md; local evidence kept outside git -->
<a id="protocol-diagnostics-2026-09-30"></a>

## gRPC diagnostic capture review


Scope: the first gRPC diagnostic capture slice and the preceding shared-analysis
and collector foundations. This is not acceptance of the complete monitoring,
preparation, protocol-analysis, or Parquet-import program.

<a id="protocol-diagnostics-2026-09-30--findings-fixed"></a>
### Findings fixed

- Bidirectional streams could deadlock while writing before reading. They now
  send and receive concurrently, cancel blocked I/O, and join the sender.
- Partial streams could lose their actual status or replace it with local
  cleanup cancellation. Synchronous terminal observations take precedence over
  delayed transport events; receive caps leave remote completion unavailable.
- A malformed client-stream request could invent an RPC `Unknown` status.
  Local decoding failures now preserve partial counts without fabricating status.
- Missing first responses diluted legacy timing averages. Optional measurements
  now use independent denominators; measured zero remains a sample.
- Mutable optional diagnostics needed deep copies through metrics, wire, archive,
  and snapshot projections. Validation bounds untrusted identities and values.
- The new diagnostics were initially added only to a legacy summary surface.
  The active run Diagnostics view now includes the panel as well.
- Generated API contracts treated diagnostics as opaque objects. Bounded model
  discovery now emits typed observations and distributions, with route-level
  contract regression coverage.
- Import-preview credential regexes performed quadratic scans of long fields.
  Boundary-aware scans preserve quoted, encoded (including encoded initial
  characters), numeric-prefix, and suffix
  credential masking. The existing large-input regression remains enabled.
- The new browser fixture could close its request context during a polling
  handler. Teardown now drains handlers before the context is disposed.

<a id="protocol-diagnostics-2026-09-30--verification"></a>
### Verification

- Real gRPC fixtures cover all four modes, early server rejection, deadlines,
  cancellation, receive caps, malformed later requests, and full-duplex payloads.
- Protobuf, local/distributed aggregation, optional-zero JSON, and real
  JSONL-to-Parquet conversion have regression coverage.
- Targeted protocol race tests passed three consecutive runs after the final
  status fix. Generated API contract tests and documentation checks passed.
- Dashboard typecheck, lint, and the embedded production build passed. The
  complete unit gate passed 435 files and 4,640 tests again after the final
  encoded-name redaction fix.
- Narrow-screen browser capture was inspected. The complete dev-admin browser
  cycle had 170 passes, four configured skips, and the polling-teardown failure
  described above. After the fix, the new workflow passed five consecutive runs
  in each auth profile. The complete local-auth cycle passed 91 tests with one
  configured private-fixture skip. The original cycle's failed exit is retained;
  the focused repetitions verify the subsequent fix.
- After the final UI rebuild, the gRPC diagnostic and protected credential-edit
  workflows both passed in dev-admin and local-auth (two checks per profile).
- The final `go test -p 1 -ldflags=-w ./...` passed, including the API package
  (478.5 seconds). An earlier overlapping Go/dashboard build failed because Vite
  replaced embedded assets during Go compilation; the successful run used stable
  assets. No Go source changed after that pass's gate.

That pass's final self-review found no additional actionable defects in this
slice. The subsequent review below records later findings and their fixes.

<a id="protocol-diagnostics-2026-09-30--follow-up-review-and-fixes"></a>
### Follow-up review and fixes

- The engine's history estimator omitted the new inline gRPC histogram arrays.
  A regression retained 92,012,544 bytes of diagnostic structs under the
  67,108,864-byte history budget. The estimator now includes their full struct
  size across every action-metrics projection. The test checks actual retained
  diagnostic allocations, eviction, and preservation of the newest evidence.
- A stream that exceeded the decoded JSON response byte limit did not set
  `receive_limit_reached`. The shared stream receiver now marks that boundary
  as partial, preserving payload counts and keeping final status unavailable.
  A real gRPC test transfers two 3 MiB protobuf payloads whose combined JSON
  exceeds the response cap; it reproduced the missing flag before the fix.
- Both new regressions pass after the fixes. Targeted engine and gRPC race
  tests passed three consecutive runs. Formatting and documentation checks pass.
- The first full Go attempt exhausted disk space during the API tests, causing
  database/fixture failures. Verified obsolete APEX cache objects and that
  attempt's unused temporary archive sandbox were reclaimed before retrying.
  The original failed log is retained at
  `/tmp/apexload-review2-go-test.log`.
- The same attempt reported an intermittent pre-existing generator test failure
  while inspecting a concurrently disappearing temporary Parquet file
  (`TestGeneratorStagesReusableScriptWithAssignedTestID`). Fifty focused repeats
  passed without changes to that test or its implementation. This failure is
  retained as an unresolved intermittent observation, not described as fixed.
- The full `go test -p 1 -ldflags=-w ./...` rerun passed, including the API
  package (434.3 seconds) and generator package. Its log is
  `/tmp/apexload-review2-go-test-retry.log`. No source was changed to suppress
  either failure from the first attempt.
- The follow-up review found no additional actionable defects in the changed
  code. The broader roadmap remains outside this slice's acceptance.

<a id="protocol-diagnostics-2026-09-30--remaining-roadmap-scope"></a>
### Remaining roadmap scope

Run-bound durable monitor jobs, additional adapters, zero-traffic preparation,
DuckDB packaging and native imports, the 10 GB acceptance run, deep WebSocket
diagnostics, and qualified diagnostic comparisons/findings remain unfinished.
See [measurement semantics](../../PROTOCOL_DIAGNOSTICS.md) and
[the roadmap](../../ROADMAP.md).

<!-- Original record: docs/qa/reviews/roadmap-controlled-endpoints-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-controlled-endpoints-2026-10-20"></a>

## Controlled protocol endpoint review — 2026-10-20


This test-only slice extends [PROTO-B](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#proto-b--controlled-endpoint-qualification)
and the [measurement matrix](../../qa/PROTOCOL_TELEMETRY_MATRIX.md#controlled-grpc-and-websocket-endpoints).
It changes no runtime behavior or support-promotion record.

<a id="roadmap-controlled-endpoints-2026-10-20--qualified-local-fixtures"></a>
### Qualified local fixtures

- gRPC unary, client-streaming, server-streaming and bidi compare observed message/byte counts with literal independent protobuf encodings and endpoint counters. Binary payloads survive unchanged; reflection does not enter call populations.
- Default no-configured-retry and test-only grpc-go retry success/exhaustion preserve attempt payload counts, optional first response and final status. Twelve concurrent calls retain independent observations. The retry service configuration belongs to this fixture, not the authored product contract.
- WebSocket holds one post-handshake transport write at a bounded, deadline-aware gate. Tests reconcile application pending writers, elapsed send work, exact endpoint payloads, timeout without delivery and successful existing reconnect behavior. Clients close and endpoint handlers join before final duplicate-delivery checks.

<a id="roadmap-controlled-endpoints-2026-10-20--validation-and-review"></a>
### Validation and review

Applied test and race counts (`evidence/validation-counts.json`), commands (`evidence/run-checks.sh`),
runtime/library pins (`evidence/runtime.txt`), source hashes (`evidence/source-hashes.json`)
and review notes (`docs/qa/reviews/roadmap-controlled-endpoints-2026-10-20/evidence/REVIEW.md`) identify each cohort. Both affected protocol
packages run in full with `-count=1`, then again with the race detector. Repository
link/help-contract validation follows. In-app Help and embedded UI sources are
unchanged; the immediately preceding Java slice verified Help in both auth profiles.

Retained draft failures exposed two fixture assumptions: reconnect is enabled by
default, and a write deadline has timeout classification. The failure lane now
explicitly disables reconnect, a separate lane checks successful retry, and the
expected reason matches the existing contract. Review also replaced an immediate
negative delivery check with bounded endpoint draining, and strengthened concurrent
gRPC status validation. Original failures and passing repetitions remain separate.

<a id="roadmap-controlled-endpoints-2026-10-20--limits"></a>
### Limits

One local darwin/arm64 loopback cohort. gRPC bytes are uncompressed protobuf payloads,
not HTTP/2 transport bytes; concurrent calls do not prove a measured codec-overlap
percentage. WebSocket pending writers are application counts, not native TCP/kernel
queue depth. This does not qualify production overhead, separate-host performance,
the full query/report/baseline consumer chain, the Java JVM helper, Linux/Windows
native behavior or real-device profiles. Those roadmap gates remain open.

Evidence checksums (`evidence/SHA256SUMS`) cover the retained bundle. No private HAR
or converted capture content is included; importer behavior is unchanged.

<!-- Original record: docs/qa/reviews/roadmap-coordinated-published-clock-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-coordinated-published-clock-2026-10-19"></a>

## Roadmap implementation: coordinated starts across published-clock drift


Date: 2026-10-19. Scope: a bounded [TEL-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age) clock-domain correction. Base: `dd397bbd7` with upstream `609bf1b1a` integrated.

<a id="roadmap-coordinated-published-clock-2026-10-19--finding-and-contract"></a>
### Finding and contract

The integrated full Go run encountered a roughly 39-minute wall/monotonic discontinuity. Two real-generator adoption tests failed because a heartbeat-derived offset was added to the controller's deadline and then compared directly with generator wall time. That offset describes the generator's monotonic published clock, which intentionally remains anchored while work is assigned. The elapsed-barrier refusal was correct; the conversion into its clock domain was wrong. The failed full run is retained, not marked passed or dismissed as a flaky test.

At RPC receipt, the generator now maps the controller barrier into its published clock, obtains the remaining interval there, then expresses that interval as a local wall-clock deadline. Transit cannot extend the common deadline. Retries keep the once-translated deadline. The active telemetry anchor, elapsed/far-future refusals, epoch/supervision fences, wire format and legacy relative-time path remain unchanged.

Deterministic tests cover positive/negative 39-minute, five-second and subsecond differences, elapsed deadlines, asymmetric transit and input/anchor non-mutation. A real gRPC heartbeat probe and real HTTP generator execution verify the resulting deadline within the measured probe uncertainty. This does not qualify all possible later clock steps, end-to-end telemetry latency, real multi-host recovery or reference environments.

<a id="roadmap-coordinated-published-clock-2026-10-19--validation-and-review"></a>
### Validation and review

Draft overlay regressions fail on the original code and pass with the correction. Real execution, existing barrier tests and focused race checks passed against the draft. The applied-source generator package passed (47.892 s), focused race checks passed (6.114 s), and both original adoption regressions passed three consecutive runs (19.541 s). Four native analytics API compatibility workflows passed (24.548 s). The corrected full Go suite passed all 168 tested packages, with 10 skipped/no-test packages and no failures; see package results (`evidence/corrected-full-go-results.json`). The generated telemetry inventory, documentation/help checks and whitespace checks passed. Final source, safety-fence, test-cleanup and scoped-diff review found no further actionable issues. The unavailable reference and multi-host lanes remain pending. The initial full-suite failure is recorded in package results (`evidence/initial-full-go-results.json`).

<!-- Original record: docs/qa/reviews/roadmap-durable-cursors-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-durable-cursors-2026-10-19"></a>

## Roadmap implementation: durable controller cursors


Date: 2026-10-19. Scope: the durable controller cursor and source-window recovery slice of [TEL-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age), following [source collection provenance](#roadmap-source-windows-2026-10-19).

<a id="roadmap-durable-cursors-2026-10-19--contract-and-recovery"></a>
### Contract and recovery

The controller persists accepted collection high-water marks and their counted run/group effects in one versioned checkpoint, together with the publication/counter ledger, source requirements, receipt coverage and bounded source-window evidence. Storage checks the existing run incarnation, project/reference/creation identity, active lifecycle and execution lease term. Increasing revisions prevent a delayed write from replacing a newer snapshot. Database I/O is outside the runner mutex. Ordinary full-row saves cannot overwrite this controller-owned state.

Adoption reads durable state after securing generators and before resuming streams. It restores cursors and totals together; it does not combine a durable cursor with totals reconstructed from sampled history. A generation change preserves same-process collection deduplication while resetting the receipt-jitter bridge. Missing, incompatible or unreadable state keeps legacy unknown/lower-bound recovery. A failed write leaves the previous snapshot; a lost reply after commit is recovered from the database. Unknown payload versions remain untouched.

Source-clock windows retain collection/publication identity, source epoch, generation, admission-policy hash and available cumulative loss-counter readings. Classification remains unknown. At most 1,024 collections and 4,096 group intervals are retained; evictions are marked. The opaque checkpoint is bounded to 8 MiB; oversized writes are refused atomically. The source-epoch archive sidecar retains these windows with nanosecond precision. Its byte budget trims optional window detail before pre-existing source/counter evidence. Original generator archives and rate denominators are unchanged.

SQLite and PostgreSQL schema release 67 adds two private columns to the existing test row. The primary-key lookup needs no new index; deletion/retention follows the test row. Existing rows start without evidence. A binary whose schema ledger predates release 67 refuses that newer database under the existing downgrade guard; restore a compatible backup for a schema rollback. The application rejects unknown checkpoint payload versions without inventing measured zero or replacing them.

Receipt coverage remains `telemetry_coverage.v3` and adopted coverage remains a lower bound. Persisted source windows do not qualify cross-host clocks, recover observations never committed, prove dashboard delivery or establish an end-to-end SLA. Policy/cadence segmentation, qualified source-clock coverage, capture-to-presentation correlation and reference-workload acceptance remain open. Messaging and CloudTest outcome parity remain outside scope.

<a id="roadmap-durable-cursors-2026-10-19--validation"></a>
### Validation

Base: `8f120a25f` on `origin/main` `4f75f1ac4e5ed198995258658c3b476597a34828`; fetched and integrated before builds and the full API suite. Local macOS arm64, Go 1.26.9 and Node 22.23.3, with a task-specific Go cache. Commands and environment (`evidence/commands.txt`) record the validation setup.

- Focused API checks (`evidence/api-targeted-final.log`): checkpoint, receipt coverage, source ledger and adoption tests passed after the final fixes. Focused storage checks (`evidence/storage-targeted.log`) passed.
- Full Go suite (`evidence/full-go-summary.log`): `go test -json -p 1 -parallel 4 -count=1 -timeout=20m ./...` exited 0: 168 packages passed and 10 had no runnable tests. The summary retains all package outcomes and the SHA-256 of the raw JSON events. All seven new API tests and both new SQLite tests passed in this run.
- Real PostgreSQL checks (`evidence/postgres-final.log`): upgrade from the previous schema, lease/incarnation fencing, concurrent revision ordering and schema fingerprint checks passed using PostgreSQL 16 in a disposable local container.
- Acceptance inventory (`evidence/inventory-final.log`): freshness check passed. The nine new default-build tests are included in the maintained telemetry race matrix; `bash -n scripts/run-optin-suites.sh` passed.
- Help tests (`evidence/help-tests-final.log`): 34 content and contextual-help tests passed. The renderer fragment/section-ownership scan completed without errors.
- Dashboard build (`evidence/dashboard-build-final.log`): both TypeScript checks and Vite bundling passed. The existing large-chunk advisory remains. Asset generation completed before the full Go suite and race matrix; generated bundles are excluded from the source commit.

The final telemetry race matrix (`evidence/race-matrix-final.log`) passed for `pkg/api`, `pkg/distributed/generatorrpc`, `pkg/analysis` and `pkg/storage`, including all nine new default-build tests. No races were reported. The exact maintained selector ran serially with `GOGC=20`, `GOMAXPROCS=2`, `-p 1` and `-parallel 4`. Its third-party `go-m1cpu` compiler warning is retained. Documentation checks (`evidence/docs-final.log`) passed for 252 active Markdown files. Retained text logs normalize trailing whitespace only.

Browser verification used the built controller and a disposable local database in both dev-admin and local-auth profiles. Local-auth search (`evidence/help-local-search-click.txt`), deep-link reload (`evidence/help-local-reload.txt`), the CI cross-guide link and focus (`evidence/help-final-linked-page.txt`), and the final dev-admin reload (`evidence/help-final-devadmin-reload.txt`) worked. The 760-pixel layout check (`evidence/help-final-layout.txt`) found no horizontal overflow and confirmed the corrected unknown/recovery wording. The final screenshot (`evidence/help-final.png`) was visually inspected. The browser session was closed (`evidence/browser-close.txt`).

The existing generation/clock-order assertion exposed an overly strict intermediate guard; its failed run is retained (`evidence/api-generation-review-failure.log`), and the assertion was preserved when fixing the implementation. Two full-suite attempts were deliberately stopped for review fixes; their hashes and interruption reasons (`evidence/interrupted-attempts.txt`) distinguish them from the successful final run.

<a id="roadmap-durable-cursors-2026-10-19--review"></a>
### Review

Self-review covered atomicity, lease/generation/incarnation fences, concurrent write ordering, restart and takeover behavior, bounded retention, unknown/legacy data, archive budgets and unchanged measurement semantics.

Findings fixed during implementation and review:

- Preserve collection identity across a generation change without bridging the takeover gap. Persist generation and process-start identity so stale processes are rejected within a term, while a genuinely newer term still outranks another process’s clock. The existing `TestTelemetryCoverageNewEpochRestartsCollectionIdentity` caught an overly strict clock guard; its assertion was preserved.
- Stream installation must not replace a restored accumulator with an earlier copy after a terminal collection has already advanced its cursor. The new regression exercises that interleaving.
- Require version-1 totals and completeness flags explicitly. Missing fields, malformed boundaries, incompatible identities and unknown payload versions cannot become measured zero; read failures and unknown versions prevent replacement of retained evidence.
- Apply the same structural retention limits to checkpoint writers and readers. A ledger beyond those limits leaves its previous recoverable cursor/effects snapshot intact.
- Bound total retained group intervals as well as collection count. Keep optional window details out of live-summary copies, and trim those details before existing source/counter records when the archive sidecar’s byte allowance is tight.
- Correct earlier help wording that still described all durable recovery as open or all pre-adoption observations as lost. Preserve the distinction between recovered provenance and qualified source-clock coverage.

The final review round found no remaining issues after those fixes. It rechecked the tested implementation, test assertions, migration fingerprint, archive limits, adoption interleaving, maintained test selectors, help wording and staged file scope. `gofmt` and `git diff --cached --check` were clean. The staged evidence review also corrected a trailing blank line and explicitly included validation logs excluded by the repository's general log-file ignore rule.

<a id="roadmap-durable-cursors-2026-10-19--cleanup-and-isolation"></a>
### Cleanup and isolation

Cleanup evidence (`evidence/cleanup.log`): task-specific Go/test and compiler caches plus worktree Vite caches were cleared, reclaiming 9.49 GiB. Unused Docker build-cache pruning reclaimed 0 bytes. The disposable PostgreSQL container/volume and owned browser/controller sessions were closed; existing service containers, dependencies and application data were preserved. The tested dashboard bundle is retained outside the commit.

Isolation check (`evidence/scope.txt`): the primary checkout’s unrelated launch configuration and dashboard lockfile retain their original hashes. The commit contains only this slice’s implementation, tests, documentation and evidence.

<!-- Original record: docs/qa/reviews/roadmap-java-body-conversion-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-java-body-conversion-2026-10-19"></a>

## Roadmap implementation: Java HTTP body conversion


Date: 2026-10-19. Scope: a bounded byte-preservation prerequisite for [PROTO-A/PROTO-B](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#proto-a--finish-truthful-measurements-and-propagation). Base: `0c51cc296`.

<a id="roadmap-java-body-conversion-2026-10-19--contract-and-correction"></a>
### Contract and correction

The embedded Java HTTP adapter decodes an authored base64 body exactly once. The shared HTTP adapter then receives opaque bytes and applies any explicitly requested content compression. Caller-owned actions remain unchanged. Replay evidence retains the transformed-body marker so repair cannot treat decoded bytes as editable offsets in the authored base64 text. A redacted snapshot body stays redacted.

Previously, an explicit action-level `body_encoding: base64` survived the Java conversion and triggered a second conversion in HTTP. Binary serialized bytes failed before traffic; opaque bytes that happened to be valid base64 silently changed. The implicit default also omitted transformed-body provenance. Independent endpoint fixtures reproduced these failures before the fix; the failing run is retained (`evidence/java-body-before.log`), including its raw non-UTF-8 fixture bytes.

No JVM object interpretation, new helper, diagnostic schema, timing-field reinterpretation or target probe is introduced. Legacy conversion timings retain their meanings. Versioned byte/conversion measurements, propagation, JVM object-footprint measurement and wider protocol qualification remain open.

<a id="roadmap-java-body-conversion-2026-10-19--validation"></a>
### Validation

- Java HTTP and HTTP package tests (`evidence/java-body-tests.log`) passed, including request compression and existing binary response mapping.
- Final Java HTTP and correlation-repair package tests (`evidence/java-body-final.log`) passed. Six endpoint cases cover implicit/explicit binary, base64-looking opaque bytes, whitespace around the encoding, empty input and gzip. Additional cases preserve preflight rejection of malformed/truncated base64 and redacted HTTP error snapshots.
- Java HTTP race tests (`evidence/java-body-race.log`) passed. The independent endpoint counter is atomic and no source body is mutated.
- Final source/test review found no further actionable issues after adding the transformed-body provenance assertion and synchronization in the test counter. This patch is confined to the Java HTTP adapter and its tests; the shared HTTP runtime is unchanged.
- Documentation links and whitespace checks pass. No help catalog or dashboard changes occur in this slice; [the preceding both-profile help verification](BROWSER_SLICES_2026-10.md#roadmap-monotonic-browser-timing-2026-10-19) remains applicable.

Validation ran on Go 1.26.9, macOS arm64, with local disposable HTTP endpoints. Evidence hashes (`evidence/SHA256SUMS`) preserve both the reproduction and passing runs. Build caches remain available for subsequent slices. External reference/platform qualification remains pending because the required environments are unavailable.

<!-- Original record: docs/qa/reviews/roadmap-java-encoding-admission-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-java-encoding-admission-2026-10-19"></a>

## Java encoding admission and protocol regression inventory — 2026-10-19


A PROTO-A/PROTO-B slice from the [implementation spec](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#proto-a--finish-truthful-measurements-and-propagation).

An explicitly unsupported Java HTTP body encoding previously bypassed admission
when the body was nil or empty. The adapter now rejects it before sending target
traffic. Supported empty bodies, default/trimmed base64, existing preflight
snapshots and legacy conversion timing meanings remain unchanged.

An independent loopback endpoint reproduces eight pre-fix nil/empty bypasses.
The regression also checks nonempty rejection, caller input ownership and valid
empty-body execution. No customer capture was replayed.

<a id="roadmap-java-encoding-admission-2026-10-19--validation"></a>
### Validation

- All Java HTTP, gRPC and WebSocket package tests passed on the applied source:
  80 top-level tests, zero skips or failures.
- Eight targeted generator DTO/API/archive propagation tests passed.
- The [measurement inventory](../../qa/PROTOCOL_TELEMETRY_MATRIX.md) maps the exact
  fixtures and leaves missing consumer, helper, overhead and platform gates open.
- The validation summary (`evidence/validation-summary.json`), compressed logs,
  test binary hashes (`evidence/test-binary-hashes.json`),
  source hashes (`evidence/source-hashes.json`) and runtime (`evidence/runtime.txt`)
  retain the local darwin/arm64 cohort.

`before-stable.log.gz` is the intentional pre-fix failing experiment.
`before.log.gz` is an earlier overlay import-resolution tooling failure and is
not evidence of a product regression. Overlay passes were followed by the
applied-source run. The API propagation run preceded the separate recovery slice.

The private ZIP's actual multipart HAR import was independently verified in the
[scenario review](AUTHORING_AND_IMPORT_SLICES_2026-10.md#roadmap-scenario-prerequisite-2026-10-19), with
unchanged structural results. This slice changes no importer code.

<a id="roadmap-java-encoding-admission-2026-10-19--review-and-limits"></a>
### Review and limits

The source and test review found no remaining issues: validation precedes HTTP
for every body shape, supported defaults remain executable, caller ownership is
preserved and legacy timing/snapshot fields keep their meanings. Documentation
review maps only tested paths and retains the missing qualification gates.
See review rounds (`evidence/review.txt`). This change
adds no JVM helper, object footprint measurement or release performance claim.
Native platforms, every report/query consumer and remote reference environments
remain unqualified. Local build caches are retained for subsequent slices and
will be cleaned after the session's dependent validation is complete.

<!-- Original record: docs/qa/reviews/roadmap-java-representations-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-java-representations-2026-10-20"></a>

## Java opaque representation review — 2026-10-20


This slice implements the versioned embedded Java-over-HTTP byte and base64-work
cohort in [PROTO-A/PROTO-B](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#proto-a--finish-truthful-measurements-and-propagation).
The [measurement matrix](../../qa/PROTOCOL_TELEMETRY_MATRIX.md#versioned-java-opaque-representations)
links acquisition, propagation and consumer fixtures.

<a id="roadmap-java-representations-2026-10-20--delivered-contract"></a>
### Delivered contract

Optional versioned diagnostics distinguish runtime input, decoded request, first
prepared HTTP body and retained response representations. Base64 work and its
enclosing interval remain separate from JVM codec work. Action summaries preserve
own populations, missing values, measured zero, compression and bounded exact sums.
Typed RPC, live metrics, SQLite/PostgreSQL history, JSONL/Parquet, native queries,
baseline comparisons, reports and dashboard exports retain those meanings.

Native queries require one exact action and bounded raw evidence. Ratios divide
paired sums; zero denominators and incomplete windows remain unscored. Baseline
comparisons are advisory and require compatible complete action evidence. Each Java
summary's `population` counts actual java_http actions only, excluding same-name
transaction wrappers and other protocols' same-named actions; legacy records
without that population remain unavailable. Existing transaction
totals and schema migration fingerprints retain their earlier meanings.

<a id="roadmap-java-representations-2026-10-20--validation-and-review"></a>
### Validation and review

- Final full Go gate: 168 packages, 17736 top-level tests and 17622 subtests passed; 33 top-level tests skipped. See exact test counts and failures by cohort (`evidence/validation-counts.json`) and final full output (`evidence/final-clean-full-go.jsonl.gz`). A pass here does not convert environment-gated skips to qualification.
- Final dashboard typecheck, lint, build and 720 unit files / 8,032 tests passed. The broad cycle passed dev-admin 329 tests (6 skipped) and local-auth 268 (1 skipped). After review fixes, the affected four Java workflows plus Help passed dev-admin 16 (1 skipped) and local-auth 17. Later screenshot-framing changes passed their two affected workflows separately in both profiles; the resulting Java evidence, independent counts, absence and help were visually reviewed.
- Affected protocol, HTTP, Java HTTP, engine, RPC, analysis, storage and API race checks passed. Storage/API were repeated separately after linker disk exhaustion. Targeted API, native metric/unit fixtures, generated contract checks, analytics inventory, documentation QA and actual PostgreSQL migration/history tests passed.
- The private attachment imported successfully through the real endpoint: HTTP 200, 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, 4 warnings, zero DSL issues and 264 protected requirements. Aggregate comparison (`evidence/har-compatibility-final.json`) matches the preceding slice exactly except the randomized script hash. Original ZIP hash is unchanged. Neither target replay nor converted private payload retention occurred; only the aggregate and compatibility harness are preserved.

Commands and cohort boundaries (`docs/qa/reviews/roadmap-java-representations-2026-10-20/evidence/commands.md`), runtime versions (`evidence/runtime.txt`),
final source/asset hashes (`evidence/final-source-hashes.json`) and review rounds (`docs/qa/reviews/roadmap-java-representations-2026-10-20/evidence/REVIEW.md`)
make the repetitions distinguishable. Earlier failures remain in their own logs:
unit-resolution and compatibility-barrel defects were corrected; archive download
refusals were caused by the real 1 GiB free-space guard and repeated after reclaiming
obsolete build archives. The guard was preserved. Staging the new tests changed the tracked QA inventory; its generated dashboard was refreshed and the complete gate repeated. The CSV browser assertion was
corrected to match quoted export cells. A first local-auth selection covered Help
only; it is not counted as Java coverage.

Review fixed the action denominator, direct JSON byte bound, native unit lookup,
coverage consistency, text timing/availability projections, independent baseline
CSV counts, auth-profile selection and import grouping. The final source review
found no further issues; staged files and evidence are checked before commit.

<a id="roadmap-java-representations-2026-10-20--limits"></a>
### Limits

This is one local darwin/arm64 embedded opaque-byte cohort. UI workflows use
controlled API evidence with real navigation/authentication and panel persistence.
JVM object footprint, actual JVM codec execution, an approved helper, measured
production overhead, complete distributed source-window qualification, reference
hosts, Linux/Windows native runners, real TruClient exports and multi-host recovery
remain open. No acceptance promotion or full PROTO-A/PROTO-B closure is claimed.

Evidence checksums (`evidence/SHA256SUMS`) cover the retained bundle. Source manifests
exclude this evidence directory to avoid recursive hashing.

<!-- Original record: docs/qa/reviews/roadmap-render-population-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-render-population-2026-10-22"></a>

## Publication identity and render population — 2026-10-22


This [TEL-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age) gives every live publication an identity that reaches the browser. It defines the eligible publication population and accounts each eligible publication for one reference client. A TEL-B consumer states the result in the run report. Before this, the advisory browser-render stage sampled what a visible tab happened to render. Hidden receipts, superseded callbacks and failed reports were unaccounted gaps.

<a id="roadmap-render-population-2026-10-22--contract"></a>
### Contract

- **Identity.** Each live point a controller installs carries `telemetry.publication` (`TelemetryPublication`): the controller's publication `epoch` for the run, a `sequence`, `eligible`, and the oldest and newest collection times it includes.
  - **Epoch:** random per publishing recorder. Re-adoption by the same recorder (`adopted`) starts a new one.
  - **Collection times:** each is on its generator's clock, qualified as `sample_age_quality` says. They are omitted when the point reports no collection time.
  - The fleet's newest collection time is chosen on the same corrected scale as its oldest.
  - Only installed points get an identity: it is assigned under the same lock and guard that install the point, so a point a guard rejects gets none.
- **Eligibility.** A publication is eligible when it carries a collection no earlier publication carried.
  - **Distributed point:** ineligible only when every generator window it carries was already published (`distributedPublicationRepeatedLocked`, decided in the install guard before `publishedWindows` advances). A generator whose collection is not identified, an older peer or a batch without telemetry, is never proved repeated.
  - **Local point:** ineligible when it repeats the previous point's collection time.
  - Replayed and lifecycle-only envelopes are therefore excluded.
- **Client outcomes, best effort.** A reporting tab is one view of the run page. It reports the named publications it receives by identity:
  - `visible`, with its measurement, when rendered;
  - `superseded`, when a newer publication rendered first (`background` if that receipt was received or pending while hidden);
  - `background`, when the publication was received or painted while hidden.

  Further rules:
  - Unmeasured outcomes are sent only after the tab measured a visible render; those received earlier are reported with it.
  - Receipts logged before the one on screen when reporting starts are dropped, not reported. The on-screen receipt and later ones are kept.
  - A re-delivered publication replaces its unresolved receipt; one re-delivered after its first copy was reported is not reported again. The server counts a publication named twice in one report as rendered if either copy rendered.
  - Visible and unnamed samples spend the viewer's sample budget, and every report spends at least one; a named publication's unmeasured outcome spends none.
  - A malformed publication name refuses the report with 400.
- **Reference client.** The first tab whose report includes a visible render of a publication the controller still holds in the current epoch. It is keyed by the viewer's subject plus the tab identifier, so another viewer cannot report as it, and is exposed only as an opaque identifier.
- **Population** (`TelemetryRenderPopulation`, live as `telemetry.render_population` and archived as `telemetry_render_population` in the manifest; never on history or interval rows). Declared as `rendered_publications`.
  - Within the reference client's reported sequence range, every eligible publication is exactly one of: `rendered`, `superseded`, `unqualified` (by reason), or `unreported`.
  - **Running counters:** the range is accounted as it grows, so each read is constant-time. Eligible publications evicted past the range's end while it is open become `unreported` once the range passes them.
  - **Retention:** the ledger holds the newest 4,096 publications, but a publication is accounted before it leaves, so eviction loses no classification. A later report of an evicted publication counts as `unknown`.
  - `published_eligible`, `ineligible` and `unknown` complete it. These are delivery outcomes, separate from accepted-record loss.
  - `sampled_from` records when the accounting began: the run's start, or this controller's adoption of the run.
- **Stage interaction.** The reference tab's unmeasured outcomes count as `browser_render` exclusions when its ledger records them. Other tabs' do not, and publications never reported do not either. Any tab's visible render refused as stalled or implausibly timed is still excluded, as before. The advisory criterion's exclusion-aware bound is therefore often inconclusive while the reference tab is hidden or busy. It is advisory, so the verdict is unaffected.
- **Reports (TEL-B consumer).** The run report's telemetry acceptance section states the archived population in HTML and PDF. Each of its figures is in the CSV (`telemetry_render_population` rows). A population accounted from an adoption says its publications were counted since then, not in all.

<a id="roadmap-render-population-2026-10-22--validation"></a>
### Validation

Go:
- `TestTelemetryRenderLedgerAccountsTheReferenceClient` covers:
  - pinning, including no pin for an unheld publication;
  - non-reference tabs and first-outcome-wins;
  - a publication named twice;
  - in-range late reports;
  - ineligible and unknown reports;
  - the exactly-once identity.
- `TestTelemetryRenderLedgerAccountsPastEviction` covers pending evicted publications, a whole evicted range, unplaceable reports and re-adoption, accounted from the adoption.
- `TestLivePublicationsCarryIdentityAndEligibility` covers sequences, eligibility, collection bounds and no fabricated bounds.
- `TestDistributedPublicationRepeatedOnlyWhenEveryCollectionWasPublished` covers the repeat check, including a batch without telemetry.
- `TestTelemetryCollectionReplayDoesNotReageTerminalMetrics` covers the install-guard wiring through a real distributed revision, under legacy and sequence identities: a new collection's publication is eligible, and a terminal replay's is ineligible with the next sequence. `TestLivePublicationsCarryIdentityAndEligibility` also shows that a point a guard rejects takes no sequence.
- `TestClientRenderReportAccountsPublications` covers the report handler, the live projection, the manifest and the reference-only exclusions.
- `TestClientRenderReportGuardsTheReferenceAndTheBudget` covers malformed names, unknown fields, a spoofed tab identifier, the budget and archived rows.
- `TestTelemetryAcceptanceReportStatesTheRenderPopulation` covers the HTML, PDF and CSV report output, including a population accounted from an adoption.

Dashboard:
- the reporter: superseded, rendered and background outcomes; batching into one render with no double reports; nothing reported from before activation or before a visible render;
- the receipt log: order, validation, bound, per-run resolution, replacement of re-delivered publications, and discarding.

End to end: `workflow-telemetry-stages.spec.ts` asserts the mixed report and the live and archived population in both authentication profiles.

The telemetry acceptance inventory has a render-population row. Its Go tests are in the telemetry fault matrix selector. The generated API contracts (both swagger copies and `openapi.ts`) are regenerated and verified.

<a id="roadmap-render-population-2026-10-22--review"></a>
### Review

Separate controller and dashboard reviewers read scratch snapshots.

- Rounds 3 and 4 found no high, medium or low issues. Their nits were fixed: discard ordering by log position; a re-delivered copy resolving its twin; a test for the hidden-before-paint gate; the wiring and guard tests and their listing; a guide parenthetical.
- Round 2 found two mediums, both introduced by the round-1 budget fix: unnamed non-visible samples skipped the budget, and a non-reference tab's refused visible renders left the stage. It also found lows: the newest bound was clamped on raw clocks; a re-delivered publication could be measured twice; a stale `truncated` type; a hidden-before-paint outcome bypassed the measurement gate; the guide's `unknown` definition; the undeclared reference tab. All fixed.
- Round 1 found:
  - **Highs:** the generated contracts were not regenerated.
  - **Mediums:**
    - outcome-only entries spent the viewer budget, and hidden non-reference tabs reported every publication;
    - eviction discarded final accounting;
    - the reference tab identifier could be reused by another viewer;
    - receipts logged while reporting was off were reported superseded;
    - an end-to-end assertion expected visible samples only.
  - **Lows and nits:**
    - malformed names were silently dropped;
    - re-adoption kept the epoch;
    - archived rows kept the population;
    - a re-broadcast could show a rendered publication as superseded;
    - bounds were fabricated without a collection time;
    - batches without telemetry escaped the repeat check;
    - labels depended on batching, and the docs overclaimed;
    - the StrictMode reset dropped an outcome;
    - the newest collection time was on an inconsistent scale, and comments were stale.
  - All of these were fixed.

<a id="roadmap-render-population-2026-10-22--remaining"></a>
### Remaining

- **Window starts.** The bounds are collection times. The oldest included observation is up to one window earlier, and per-window starts are not carried yet.
- **Durability.** The accounting lives in the publishing controller's memory.
- **Declared reference.** The reference tab is chosen (the first to report a render), not declared, and is not guaranteed to stay in the foreground. A declared foreground reference client remains open.
- **Clocks and presentation.** It does not qualify browser clocks, prove visible presentation (the callback is still a paint opportunity) or establish the capture-to-visible-render distribution. Authenticated generator→controller→browser clock mappings and the exclusion-aware end-to-end bound remain open in TEL-A.

<!-- Original record: docs/qa/reviews/roadmap-source-windows-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-source-windows-2026-10-19"></a>

## Roadmap implementation: source collection windows


Date: 2026-10-19. Scope: the next bounded prerequisite of [TEL-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age), following [replay-safe receipt coverage](#roadmap-telemetry-coverage-2026-10-19).

<a id="roadmap-source-windows-2026-10-19--contract-and-scope"></a>
### Contract and scope

`WindowMs` remains the existing rate denominator: final drains can clamp it to the publication cadence, and multi-group batches can use an effective duration to preserve additive rates. It cannot establish actual source observation boundaries.

The generator now captures each result buffer's start and end boundary under the drain lock, using elapsed monotonic time mapped to the generator's published clock. Both endpoints use one clock anchor. These describe buffer observation, including idle intervals; they are not action start/end times or a qualified mapping onto controller time. Unknown initial boundaries stay unavailable.

Additive protobuf fields `MetricsBatch.collection_windows` and `collection_windows_complete` carry group names and nanosecond timestamps. Each current collection includes only the groups collected in it; lifecycle replays retain the same intervals and collection sequence. Direct publications without measured boundaries cannot inherit earlier intervals. Source epoch, execution generation and collection sequence scope the retained archive evidence.

Wire and archive projections retain at most 256 intervals per collection; group names retain the existing 512-byte limit. Sender selection is deterministic by group name. Empty, missing, invalid, duplicate or bounded-out evidence makes the complete flag false. The flag describes completeness of the collection's drain cohort, not delivery, exact workload coverage or clock qualification. Existing result, aggregate and rate evidence is preserved when provenance is incomplete.

The generator archive's JSONL telemetry and Parquet `telemetry_json` retain the same source identity and boundaries, including the process epoch before the publication broker stamps its envelope. Old messages and archives remain readable with provenance unavailable. No REST, DSL, database migration, policy-default or Parquet-column change is introduced. Receipt-time coverage remains `telemetry_coverage.v3`.

Durable controller collection cursors, source-window recovery across controllers, policy/cadence segmentation, qualified capture-to-render correlation and reference-workload qualification remain open. These raw intervals do not close TEL-A. Messaging and CloudTest outcome parity remain outside this slice.

<a id="roadmap-source-windows-2026-10-19--validation"></a>
### Validation

Base: `57c7ff947` on top of `4f75f1ac4e5ed198995258658c3b476597a34828` (`origin/main`, fetched and integrated before building and before the full API suite). Local macOS arm64, Go 1.26.9 and Node 22.23.3. Go uses a session-specific cache, `GOMAXPROCS=3` and `-p 1` to bound compilation resources. The full Go summary extracts package results and the failed test output from JSON events, with the raw-event SHA-256 retained. Other retained logs normalize trailing whitespace only.

- Focused source-window tests (`evidence/targeted.log`): all six new tests passed, including the physical Parquet round trip and malformed-wire cases.
- Full Go suite summary (`evidence/full-go-summary.log`): `go test -json -p 1 -parallel 4 -count=1 -timeout=20m ./...` completed with 167 packages passing and one failing package. Its sole failed test was the existing `TestScriptContentRevisionConcurrentInitialization`, which encountered `SQLITE_BUSY` while two stores initialized the same database. The full run therefore exited 1; it is not recorded as an unqualified pass.
- Storage repetitions (`evidence/storage-repeat.log`): the failed test subsequently passed 20 consecutive runs. The complete storage-package retry (`evidence/storage-retry.log`) also passed. Scope check (`evidence/storage-scope.txt`): storage has no changes in this slice and depends on neither changed production package. The intermittent initialization issue is retained as an existing validation limitation; no unrelated storage fix is included.
- Help tests (`evidence/help-tests.log`): all 34 content and contextual-help tests passed after the guide correction. The renderer's fragment/section-ownership scan found no errors.
- Documentation check (`evidence/docs.log`): passed for 251 active Markdown files.
- Acceptance inventory (`evidence/inventory.log`): regenerated and passed; all six new tests are included in the maintained telemetry race selector. `bash -n scripts/run-optin-suites.sh` passed.
- Protobuf reproducibility (`evidence/protobuf-freshness.txt`): repeated generation produced identical output using protoc 5.29.3 and protoc-gen-go 1.36.11.
- Final dashboard build (`evidence/dashboard-build-final.log`): both TypeScript checks and Vite bundling passed after the help-link correction. The existing large-chunk advisory remains. Asset generation finished before the final race run.

Final telemetry race matrix (`evidence/race-matrix.log`): the exact selector maintained in `scripts/run-optin-suites.sh` passed for `pkg/api`, `pkg/distributed/generatorrpc` and `pkg/analysis`, including all six new tests. No races were reported. The retained log includes the command; this serial run used `GOGC=20`, `GOMAXPROCS=2`, `-p 1` and `-parallel 4` to keep memory bounded.

Earlier full-suite (`evidence/full-go-interrupted.log`) and race (`evidence/race-matrix-interrupted.log`) attempts were deliberately interrupted during compilation because concurrent compilation caused memory pressure. They are incomplete attempts, not passes. The large API package was then successfully precompiled with `GOGC=20` and `GOMAXPROCS=2` before restarting the full suite with its normal test settings. No source or assertion was weakened.

<a id="roadmap-source-windows-2026-10-19--review"></a>
### Review

The implementation review checked drain lock ownership, per-collection group resets, lifecycle/terminal replay, idle and unknown boundaries, preservation of rate denominators, bounded allocation, malformed and legacy wire input, archive identity before broker stamping, and physical Parquet round trips.

Findings fixed during implementation and review:

- The original `_windows.go` filename was interpreted by Go as Windows-only source. Renamed it and its test file to the platform-neutral `collection_window.go` / `collection_window_test.go`, and regenerated the acceptance inventory.
- Both interval endpoints must use one published-clock anchor. The end is derived from the mapped start plus monotonic elapsed time, avoiding mixed anchors if an idle heartbeat re-anchors concurrently.
- Archive verification initially checked only the Parquet projection. It now reopens the written Parquet file and verifies collection identity and nanosecond boundaries from its telemetry column.
- The weighted-rate assertion now requires the expected 1,333 ms denominator instead of merely excluding the two raw group durations. Additional malformed-wire cases cover missing endpoints, empty entries and oversized group names.
- The help renderer deliberately disables links to repository documents outside its catalog. Removed the new repository-only evidence callout from the help guide; the QA index remains the navigable repository entry point. Final dashboard generation and help validation follow this edit.

The follow-up review checked the complete scoped diff, regenerated protobuf, test inventory, evidence claims and repository/help links. No findings remain in this slice. The unrelated intermittent storage initialization failure remains disclosed above; successful retries do not claim to repair it. No unrelated working-tree changes or generated dashboard bundles belong to this commit.

<a id="roadmap-source-windows-2026-10-19--cleanup"></a>
### Cleanup

Cleared the session-specific Go build/test cache (about 4.9 GiB; 8 KiB of metadata remains), about 1.9 GiB of abandoned compiler work directories and the test executable, and the isolated checkout’s disposable Vite caches. Docker’s unused build-cache prune completed with 0 B reclaimed. Another task’s active Go run and shared Go cache were left untouched. Dependencies, source, images, containers and application data were preserved. The final dashboard build remains in `/private/tmp/apex-source-windows/dashboard-dist`; generated bundles are excluded from the commit. The tracked browser-test processes (`evidence/browser-cleanup.txt`) all exited, and no manual browser session was opened.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-budget-choice-2026-10-23/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-budget-choice-2026-10-23"></a>

## Run telemetry budget choice — 2026-10-23


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) lets a run choose the project telemetry budget policy that sets its live budgets. The [policy selection slice](#roadmap-telemetry-policy-selection-2026-10-22) did the same for the acceptance policy.

Before this slice, every run was classified by the system threshold settings. A project's budget policies were only pinned, so a team could not run with the budgets it had defined.

Scenario inheritance, the dashboard selects and `apexctl run` follow in a separate slice.

<a id="roadmap-telemetry-budget-choice-2026-10-23--contract"></a>
### Contract

- **The choice.** `telemetry_budget_policy_id` on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs names one of the run's project's telemetry budget policies (`telemetry_policy`). It sets the run's live budgets instead of the system threshold settings.
  - **Without a choice**, a run keeps the version 1 system receipt.
  - **The project's default budget policy** is still pinned only. It does not set a run's budgets, as before this slice.
- **Validation at creation** (`validateRunTelemetryBudgetPolicy`, `validateRunTelemetryPolicyChoice`):
  - The policy must be a live, valid `telemetry_policy` of the run's own project.
  - A policy the run's project lacks, including another project's or another kind's, gets `400` with the same message.
  - An over-long ID gets `400`.
  - An invalid policy gets `409`.
  - A policy whose `sustained_window_seconds` differs from live classification's fixed 15 seconds gets `409` (`telemetryBudgetPolicyDefinition`).
  - A failed policy read gets `503`.
  - Capacity estimates and previews do not judge the choice, as for the acceptance choice.
- **Persistence.** The choice is stored in `tests.telemetry_budget_policy_id` (schema release v82, `run-telemetry-budget-policy`) and reloaded with the run.
- **Revised copies.** A revised copy keeps its source's choice unless it names another, or `project_default`. An inherited choice that is no longer usable refuses the copy and names the source run's choice.
- **At Start** (`persistManagedTestStartOutsideLockLocked`, `resolveRunPolicyPinsForRun`):
  - The chosen revision is pinned with source `run_selection`, in place of the project default's budget pin.
  - `projectTelemetryBudgetPolicy` then replaces the system receipt captured at admission, in the same write-once admission record.
  - A chosen policy that is gone or unusable refuses Start (`errRunPolicyConfiguration`) and leaves the run queued.
- **The version 2 receipt** (`pkg/telemetrypolicy`, `VersionProjectPolicy`):
  - **Identity.** It is `apexload.telemetry_policy.v2` and names the policy ID, its revision and the SHA-256 of its definition (`definition_sha256`).
  - **Kept from the controller.** Cadence, cadence scope, capture time, capture boundary, classification algorithm, the 15-second sustained window and the coverage rule are unchanged.
  - **Stage limits.** Each stage takes the policy's limit. A stage the policy leaves unset uses the built-in default, as `resolveTelemetryReevaluationPolicy` does, and a stage set to `0` is disabled.
  - **Version 1 is unchanged.** Version 1 receipts and their hashes are unchanged: `definition_sha256` is omitted when empty.
  - **Validation.** `Validate` accepts version 1 only with the system ID, revision 1 and no digest. It accepts version 2 only with these:
    - a policy ID other than the system's, at most 128 bytes, valid UTF-8, without surrounding whitespace and without control characters;
    - a revision of at least 1;
    - a lowercase 64-character hexadecimal digest.
- **Live classification** reads the receipt (`runTelemetryPolicy`, `telemetryPolicyThresholds`). The chosen budgets therefore decide degraded and breached time, and with it an acceptance policy's `max_breached_known_time_fraction`.
- **CI.**
  - **Permission.** A CI run may choose a budget policy only where `allow_telemetry_policy_selection` is set, the same permission as the acceptance choice, because looser budgets can loosen a `fail_ci` acceptance policy. Otherwise the request is refused with `403`. `project_default` needs no permission.
  - **Retries.** The choice is part of the request metadata a retry under the same key must repeat. An exact retry of an accepted run returns it even after the choice is disallowed or deleted.
  - **Echo.** The response echoes the choice.
- **apexctl CI.**
  - `apexctl ci run --telemetry-budget-policy` (action input `telemetry-budget-policy`) sends the choice. It is part of the derived idempotency key only when set, and it needs controller mode.
  - A create response that does not echo the choice is a configuration error, and the accepted run is aborted.
  - The result file carries `telemetry_budget_policy_id`, and the job summary has a "Telemetry budgets" row when the run chose them.
- **GitHub Check.** The check summary, from the queued check on, says when a run chose its live budgets, naming the policy, because looser budgets can loosen a `fail_ci` criterion.
- **Remote runs.** A remote controller that does not echo the choice would classify the run by its system settings. Its run is deleted before it starts, and the create is refused (`409`).
- **Readers.**
  - `apexctl analysis telemetry-policy` (`decodeTelemetryPolicyCLI`) accepts `definition_sha256` only alongside version 2.
  - The dashboard (`telemetryPolicyModel.ts`) checks the same identity rules and verifies the version 2 digest against the controller's encoding. `TelemetryPolicy.tsx` labels a chosen project policy and shows its definition digest.
  - An older reader never presents a version 2 receipt as the system settings. An older dashboard shows it as unsupported, and an older `apexctl analysis telemetry-policy` refuses it as an invalid receipt.

<a id="roadmap-telemetry-budget-choice-2026-10-23--validation"></a>
### Validation

- **`TestProjectPolicyReceiptIdentity`** (`pkg/telemetrypolicy`):
  - accepts a valid version 2 receipt, and leaves the version 1 hash unchanged;
  - refuses a digest on version 1, version 1 with another ID, the system ID or an empty, padded, control-character or over-long ID on version 2;
  - refuses revision 0, a missing, upper-case or short digest, another sustained window and an unknown version;
  - refuses a changed revision under the old hash.
- **`TestProjectTelemetryBudgetPolicyReceipt`** (`pkg/api`):
  - **Identity.** The receipt names the revision, its digest and the controller's cadence.
  - **Stage limits.** The policy's limits are used, unset stages get defaults, and a stage set to 0 is disabled.
  - **Use.** It sets the live thresholds and gives available evidence.
  - **Untouched input.** The system receipt is not changed.
  - **Refusals.** It refuses another window, an invalid policy, another kind, revision 0, the system ID, an invalid system receipt and a version 2 base. It accepts the live 15-second window.
- **`TestRunChosenTelemetryBudgetPolicySetsLiveBudgets`**:
  - **At creation:** missing and foreign choices get the same message, and an over-long ID, another window and an acceptance policy's ID are refused.
  - **Persistence:** the choice is persisted and reloaded.
  - **Revised copies:** they keep the choice, drop it for `project_default` and take another when named.
  - **At Start:** a version 2 receipt and a `run_selection` budget pin, with the live thresholds taken from it.
  - **Without a choice:** a run keeps the system receipt and pins the project's default budget policy.
- **`TestRunChosenTelemetryBudgetPolicyUnusableRefusesStart`**:
  - a deleted policy refuses Start, and so does one changed to another window; the run stays queued with no receipt;
  - a revised copy inheriting the choice is refused, naming the source run's choice.
- **`TestCreateTestRouteChosenTelemetryBudgetPolicy`**: the route echoes the choice, a keyed retry with spaces replays, a retry naming another policy conflicts, and an unusable policy gets `409` and a missing one `400`.
- **`TestCIRunCarriesTheChosenTelemetryBudgetPolicy`**:
  - without the permission, the choice gets `403`, while `project_default` is allowed;
  - the choice is echoed and reaches its run;
  - a retry naming another policy conflicts, and an unknown policy gets `400`;
  - an exact retry after the choice was disallowed and deleted returns the run.
- **`TestChosenTelemetryBudgetPolicyStoreOutageIs503`**: both routes answer `503` during a policy-read outage.
- **`TestRunPolicyChoiceWithoutPolicyStoreNamesThePolicies`**: without a policy store, both choices are refused with the plural policy name.
- **`TestRemoteRunNeedsTheChosenBudgetPolicyConfirmed`**: an unconfirmed choice deletes the remote run and gets `409`, `project_default` needs no confirmation, and a confirmed choice is created.
- **`TestControllerCIRunNeedsTheChosenBudgetPolicyConfirmed`** (`cmd/apexctl`): the choice is sent and changes the derived key; an unconfirmed choice aborts the run as a configuration error, and a confirmed one passes.
- **`TestCIGitHubCheckNamesChosenTelemetryBudgets`** and **`TestCISummaryShowsChosenTelemetryBudgets`**: the GitHub Check (queued and completed) and the job summary name a chosen budget policy, and say nothing about budgets when the run made no choice.
- **`TestParseCIArgsTelemetryBudgetPolicy`**: flag parsing and `project_default`; local mode is refused, and the field is omitted when unset.
- **`TestTelemetryPolicyCLIProjectPolicyReceipt`**: accepts a version 2 receipt. Refuses a digest on version 1, version 2 without a digest, a null, numeric or changed digest, and the system ID.
- **`telemetryPolicyModel.test.ts`** and **`TelemetryPolicy.test.tsx`** (dashboard):
  - a version 2 receipt verifies against the Go-computed hash `f034a3ac…`, which `TestTelemetryPolicyCLIProjectPolicyReceipt` pins too;
  - the identity refusals mirror the Go ones and happen before any digest is computed;
  - a 128-byte ID, an ID Go's encoder escapes (`<`, `>`, `&`, U+2028, U+2029) and an ID with a leading U+FEFF, which Go's `strings.TrimSpace` keeps, verify against Go-computed hashes;
  - a changed digest is refused;
  - the card labels the system receipt and a chosen project policy, with its digest.
- **Existing suites.** The existing acceptance-choice, CI, telemetry and remote-run tests, plus `cmd/apexctl`, `pkg/storage` and `pkg/runcontract`, pass. `go generate ./pkg/api` and `contracts:check` are current.

<a id="roadmap-telemetry-budget-choice-2026-10-23--review"></a>
### Review

Independent reviewers worked on scratch snapshots without git. Each round reviewed the whole diff.

- **Round 1** (backend and CLI; dashboard and docs):
  - **Backend and CLI.** The `telemetry-budget-policy` action input had split the `telemetry-policy` input's `required` and `default` lines. An unavailable policy store said "policys".
  - **Dashboard.** The identity tests passed even with the identity checks removed, because the digest check refused the mutated receipts anyway; they now assert no digest is computed. The digest input lacked Go's escaping of `<`, `>`, `&`, U+2028 and U+2029, and used JavaScript's `trim()` instead of Go's whitespace set. Added cases: an empty ID, a short digest, a 128-byte ID, an escaped ID and a leading U+FEFF.
  - **Tests and docs.** The apexctl test now pins the receipt hash. The default-pin check now fails when no pin is found. Card text, spec wording and QA notes were corrected.
- **Round 2** (both scopes):
  - **CI visibility.** The GitHub Check and the apexctl job summary did not say that a run chose its budgets.
  - **Dashboard revise.** The revise modal can neither keep nor drop the budget choice explicitly. It is documented under Remaining and follows in the scenario slice.
  - **Whitespace tests.** Only plain space and U+2029 exercised the whitespace set.
  - **Wording.** Older-reader behaviour and two other phrases were reworded.
- **Round 3** (whole diff): the queued Check lacked the budget line, and the whitespace tests sampled the U+2000–U+200A range instead of covering it.
- **Round 4** (whole diff): no findings. Every caller of `ciGitHubCheckSummary` passes the run's own metadata. The dashboard's whitespace set equals Go's `unicode.IsSpace`, checked by enumerating every rune.

<a id="roadmap-telemetry-budget-choice-2026-10-23--remaining"></a>
### Remaining

- **Scenario level.** A scenario-level budget choice, inherited by its runs, schedules and plan entries. Also the scenario builder's and revise modal's selects, and `apexctl run --api … --telemetry-budget-policy`.
- **Dashboard revise.** Until the revise modal has a budget select, a dashboard revise always keeps the source run's choice. If that policy is gone or unusable, the revise is refused (`409`), and only the API can name another policy or `project_default`.
- **Older readers.** Until they are upgraded, an older dashboard shows a version 2 receipt as unsupported and an older apexctl refuses it as invalid.
- **Credential-like policy IDs.** The public archive projection scrubs credential-like text in a receipt's policy ID, as it does for pins. A project policy ID that looks like a credential would therefore show as an unsupported receipt there.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-coverage-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-coverage-2026-10-19"></a>

## Roadmap implementation: replay-safe telemetry coverage


Date: 2026-10-19. Scope: first bounded implementation slice of [TEL-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age).

<a id="roadmap-telemetry-coverage-2026-10-19--contract-and-scope"></a>
### Contract and scope

Coverage algorithm `telemetry_coverage.v3` admits only new, ordered windows into receipt-time coverage. Duplicate and late envelopes still contribute publication and loss evidence. Repeated collection identities cannot move an old observation onto a later receipt; the first valid terminal envelope retires the source without repeating its window. Older generations and older or unorderable epochs cannot retire the active source. A valid new epoch resets collection identity without bridging the restart gap. Source-epoch retention and interval retention remain bounded; omitted evidence makes coverage a lower bound.

Legacy inputs without collection identities retain receipt-window compatibility. Missing or incomparable identity after an identified collection supplies no proof of new data. Saved v2 records are unchanged; existing compatibility logic keeps different coverage algorithms separate. No RPC, REST or storage schema changes, policy-default changes, or history rewriting are required.

This is still receipt-time coverage. Durable source windows, recovery across controllers, qualified capture-to-render correlation, policy segmentation, reference-workload qualification, and the other revised-spec tracks remain open. Messaging and CloudTest outcome parity remain outside this spec.

<a id="roadmap-telemetry-coverage-2026-10-19--validation"></a>
### Validation

Retained logs normalize trailing whitespace only.

Base: `4f75f1ac4e5ed198995258658c3b476597a34828` (current `origin/main`, fetched before builds and the full API suite). Local macOS arm64, Go 1.26.9, Node 22.23.3. Go used a session-specific cache, `GOMAXPROCS=3` and `-p 1` to bound compilation resources.

- Pre-fix regressions (`evidence/regressions-before.log`) reproduce duplicate/late collection inflation, stale retirement, unbounded coverage admission and algorithm compatibility before the implementation.
- Targeted telemetry tests (`evidence/telemetry-after.log`): `go test -p 1 -timeout=20m ./pkg/api -run '^TestTelemetry' -count=1` passed before the retention review fix. The full suite and final race check cover that fix too.
- Full Go suite (`evidence/full-go.log`): `go test -p 1 -parallel 4 -timeout=20m ./...` passed: 168 packages with tests and 10 without tests. This is the standard suite; environment-gated release qualification remains separate.
- Help tests (`evidence/help-tests.log`): `npm test -- src/helpContent.test.ts src/contextualHelp.test.ts` passed all 34 tests, including catalog loading and help-link resolution.
- Documentation check (`evidence/docs.log`): `python3 scripts/check-docs.py` passed for 250 active Markdown files. The help renderer's fragment/section ownership scan also found no errors.
- Acceptance inventory (`evidence/inventory.log`): `APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1 go test -p 1 ./docs/qa -run TestTelemetryAcceptanceInventory` passed. The source inventory and exact race selector include all nine new regression tests.
- Final dashboard build (`evidence/dashboard-build-final.log`): `npm run build` passed both TypeScript checks and bundling after the guide update; Vite reported the existing large-chunk advisory. Asset generation finished before the final Go race check.

Final race matrix (`evidence/race-matrix.log`): the exact telemetry fault selector from `scripts/run-optin-suites.sh`, run with `go test -race -count=1 -p 1 -parallel 4 -timeout=30m -v`, passed for `pkg/api`, `pkg/distributed/generatorrpc` and `pkg/analysis`, including all nine new regressions. No races were reported. The log retains the complete command.

<a id="roadmap-telemetry-coverage-2026-10-19--review"></a>
### Review

The first review found that the bounded epoch ledger also needs to propagate truncation into live and archived coverage. Fixed by taking its truncation flag in the same locked snapshot and preserving the existing lower-bound acceptance semantics. Regression coverage checks that missing retained evidence cannot become an exact below-floor failure. Added compatibility and jitter-gap tests for missing, incomparable and replayed collection identities.

The follow-up review checked bounded allocation, atomic snapshots, legacy identity compatibility, source ordering and retirement, replay-induced gaps, algorithm compatibility, evidence preservation, and the test inventory. No further production-code findings remained. The test helper was aligned with the lower-bound snapshot behavior; final race verification uses that helper and the rebuilt assets.

Final review: no findings remained after the retention fix and follow-up checks. No unrelated working-tree changes or generated dashboard bundles belong to this commit.

<a id="roadmap-telemetry-coverage-2026-10-19--cleanup"></a>
### Cleanup

Cleared the session-specific Go build/test cache (about 6.3 GiB, 8 KiB of metadata remaining) and the checkout's disposable Vite caches after all validation. Docker's unused build-cache prune completed with 0 B reclaimed. Dependencies and deliverable build output were preserved; the final dashboard build is retained outside the commit. No agent-opened browser sessions remained.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-notification-runtime-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-notification-runtime-2026-10-20"></a>

## Atomic telemetry acceptance notifications — 2026-10-20


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) connects the notification outbox to the exact verified archive publication. The existing catalog completion transaction also commits either the immutable event or a fixed bounded decision. Local completion and recovery from an uploaded archive whose local source is lost use the same boundary.

The execution leader uses bounded leases and the existing notification dispatcher. A Server-owned limit covers preparation and storage completion as well as HTTP dispatch. Recipients are opaque keyed fingerprints of the complete authorized configuration; retry reuses the original payload and recipients, while changed or removed configurations revoke their delivery. Temporary settings failures remain retryable. Final lease/incarnation admission rejects stale or deleted work before dispatch. Already admitted external requests cannot be recalled, and ambiguous receiver completion remains at least once with a stable idempotency key.

The retained run's redaction rules and shared credential scrubber protect the bounded envelope. The viewer-authorized status operation exposes only fixed publication decisions, manifest/event identity, suppression count and delivery totals, with service-account scope checks. No destination, payload or credential is returned. Capacity and rejected projection decisions do not block archive completion indefinitely. Custom stores without the complete atomic capability retain the documented legacy claim-before-dispatch behavior.

Schema release 69 appends to release 68, preserving previous fingerprints. New publication state follows test/project deletion. Historical archives and legacy claims retain their semantics. The [owning guide](../../guides/TELEMETRY_POLICY_RECEIPTS.md#durable-acceptance-notifications) defines bounds and operational interpretation.

<a id="roadmap-telemetry-notification-runtime-2026-10-20--validation-and-review"></a>
### Validation and review

- Applied API/route/freshness checks: 23 top-level tests and 10 subtests passed.
- Expanded applied route/RBAC/service-account/generated-contract checks: 118 top-level tests and 304 subtests passed.
- Applied runtime race suite: 14 top-level tests and 10 subtests passed, including retry, authorization, deletion, shutdown and bounded preparation observers.
- Applied SQLite/PostgreSQL and schema checks: 17 top-level tests and 90 subtests passed. Schema release 69 preserves earlier release fingerprints and explicitly tests both publication foreign keys.
- Full Go suite: 168 packages, 17,671 top-level tests and 17,350 subtests passed. The test-only private-HAR overlay ran within this gate; all production source came from the installed checkout.
- Actual private ZIP import: HTTP 200, 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL issues; matching aggregate fields and unchanged original ZIP hash.
- API/TypeScript generation, telemetry inventory, dashboard typecheck/lint/34 Help units/build, both Help auth profiles, documentation checks and evidence checksums passed. Exact logs, input/asset/controller hashes and original failed draft fixtures are retained in the evidence bundle (`SHA256SUMS`).

The private customer ZIP was imported through the real multipart API without replay or saved converted content. Its safe aggregate result matches the preceding compatibility cohort. Random generated script/attestation hashes are excluded from equality claims; the original ZIP hash is unchanged.

Review corrected archive-store authority, capability fallback, final incarnation admission, observer bounds, and fixture shutdown/incarnation setup. Three superseded full-suite attempts exposed stale API/auth inventories and legacy-only failure adapters; their failed logs remain in the evidence bundle, with the first two marked incomplete. Corrected deletion-fence and lease-loss tests passed five race-enabled repetitions across legacy and atomic completion before the broader archive and final integrated gates. Final applied-source and staged review found no remaining actionable findings after the recorded storage, lifecycle, authorization and fixture corrections.

The fixtures use local SQLite, a disposable local PostgreSQL container and local HTTP receivers only. They do not certify external receivers, multi-host failover or reference-host performance. In-run policy segments, the full telemetry consumer/sanitization matrix and other TEL-B requirements remain open.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-notification-storage-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-notification-storage-2026-10-20"></a>

## Telemetry notification outbox storage — 2026-10-20


Storage groundwork for [TEL-B](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers). Runtime delivery and atomic archive-publication integration remain subsequent work; the existing notification path is unchanged by this slice.

Schema release 68 appends a new fingerprint without rewriting prior releases. Older binaries refuse to open this newer schema ledger; retain the database and use a compatible binary rather than deleting its migration history. The legacy claim marker also protects an already-running older controller during a mixed rollout.

The additive SQLite/PostgreSQL tables retain a versioned immutable announcement and independent delivery state for each destination. Identity binds project, run ID/incarnation, policy ID/revision, assessment digest and verdict. Enqueue locks and checks the terminal run in the insertion transaction. Repeated identical input replays; changed payload or destinations conflict. An empty destination set retains the original no-recipient decision, so later settings cannot redirect that outcome.

The complete serialized envelope is bounded to 64 KiB, including metadata and JSON escaping; each run admits at most 32 events and each event at most 64 destinations, matching the existing notification settings limit. Only opaque destination fingerprints are accepted. The eventual caller must supply a scrubbed projection and resolve fingerprints against current authorized configuration; this storage API does not sanitize arbitrary notification content itself.

Delivery uses fresh owner tokens, exact observed attempt numbers, bounded leases, eight attempts and capped exponential backoff. Expired delivery leases allow retry; they never prove that a receiver did not commit a request. Delivered, revoked and exhausted states leave the due scan. Old owners cannot complete replacement leases. Unrecognized future envelope versions remain intact and are not dispatched.

Legacy timestamp claims retain their unknown delivery outcome and suppress blind replay. New events atomically install a distinct legacy marker, which prevents older claim-based controllers from duplicating the same outcome while allowing new policy/assessment revisions in capable controllers. Changed run incarnations revoke active old deliveries without deleting their evidence. Test/project deletion cascades through events and deliveries; recreation cannot consume an old claim.

<a id="roadmap-telemetry-notification-storage-2026-10-20--validation-and-review"></a>
### Validation and review

- Applied-source full Go suite: 168 packages and 17,646 top-level tests passed, with 17,309 passing subtests. The API package passed 6,412 top-level tests in 877.438 seconds. Environment-specific skips remain recorded in the raw log.
- Applied SQLite/PostgreSQL outbox, upgrade and schema checks: 14 top-level tests and 46 subtests passed. The reviewed outbox race suite passed all seven default tests and 19 subtests.
- Generated test-store adapter freshness, generated telemetry acceptance inventory, documentation links/invariants and whitespace checks passed. This storage-only slice changes no embedded Help source or UI behavior; the preceding recovery slice's full dashboard/Help cycle remains the applicable unchanged cohort.
- The earlier full-suite failure (missing schema release and foreign-key bookkeeping) and separate API timeout are retained as superseded evidence, alongside the passing replacement run. Neither is relabeled as a pass.

Evidence checksums (`SHA256SUMS`) cover the raw compressed logs, source hashes, commands and review history. The source hashes were checked against the exact installed files after validation.

The review fixed unsupported-version admission, obsolete rows starving the bounded queue, UTF-8 identity ambiguity, nil/empty recipient replay, a destination limit below the existing settings contract, and serialized-envelope expansion beyond the byte cap. Final review status: clean after the schema/foreign-key corrections and the final source, tests, generated adapter and evidence review.

The stored envelope and leases are internal groundwork. The next runtime slice must enqueue atomically with the exact archive publication, preserve a durable failure decision on capacity/projection failure, reuse the bounded dispatcher, resolve only unchanged authorized destinations, handle shutdown and partial delivery, and scrub all outbound projections. No external receiver exactly-once guarantee or deployed multi-host qualification is claimed. No user capture or external notification destination was used by these local storage tests.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-policy-segments-2026-10-23/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-policy-segments-2026-10-23"></a>

## In-run telemetry policy segments — 2026-10-23


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) implements the specification's in-run policy changes: "explicitly requested, authorized in-run policy/cadence changes as append-only durable segments with exact effective boundary and revision. A settings edit alone does not change a running test. Retry creates one segment; failed persistence leaves the old segment active. A measurement spanning a boundary is assigned only using its actual observation times; otherwise it is unscored, never proportionally invented. Re-evaluation selects immutable segments without rewriting historical verdicts."

Before this slice a run's telemetry was classified by its admission receipt for its whole life, and the coverage record's single policy segment said so.

<a id="roadmap-telemetry-policy-segments-2026-10-23--contract"></a>
### Contract

<a id="roadmap-telemetry-policy-segments-2026-10-23--segments-and-the-receipt"></a>
#### Segments and the receipt

- The admission receipt is segment 1, held in the run's write-once initial provenance as before. Each change appends the next segment to `test_telemetry_policy_segments` (schema v84 `telemetry-policy-segments`, SQLite and PostgreSQL): primary key (test, sequence), a unique request ID per test, deleted with the test and the project. Segments are never updated.
- A segment's receipt is built like a run's: from the chosen project telemetry budget policy (version 2, `projectTelemetryBudgetPolicy`), or from the controller's threshold settings when it names `project_default` (version 1), over the run's controller fields and its cadence. Its capture boundary is `in-run-policy-segment` (`telemetrypolicy.CaptureBoundarySegment`), which `Validate` now accepts beside the admission boundary, while `ValidateAdmission` (a run's admission evidence in manifests, reports, comparison, re-evaluation and `apexctl telemetry-policy`) accepts only the admission boundary; the hash covers it, so a segment never shares a hash with an admission receipt.
- **Cadence.** A local run may change its publication cadence with the policy (`controller_publication_interval_seconds`, whole seconds 1–3600); the local broadcaster reads its cadence from the segment in force on each cycle (`engine.PublishOnCollectionEvery`). A distributed run's cadence stays one second.

<a id="roadmap-telemetry-policy-segments-2026-10-23--the-request"></a>
#### The request

`POST /api/v1/tests/{id}/telemetry-policy-segments` (editor; HA leader only) with `request_id` (1–128 bytes), `telemetry_budget_policy_id` (an ID, or `project_default`), optional `controller_publication_interval_seconds` and `expected_sequence`:

| Case | Answer |
| --- | --- |
| A new change | `201` with the segment; audit `test.telemetry_policy.change` |
| A retry with the same `request_id` (even after the run ended) | `200` with the segment it created |
| Malformed request, a policy the project lacks, another project's or kind's, an over-long ID, a distributed cadence change | `400` |
| A CI run whose project does not set `allow_telemetry_policy_selection` | `403` |
| Unknown run | `404` |
| Not running or paused, a stale `expected_sequence`, a `request_id` reused for another change, an unusable policy (invalid, or another sustained window), a stored segment whose receipt does not validate (including the retry of the change that stored it) | `409` |
| A failed policy or segment read, a follower or a lost execution lease | `503` |
| `GET` of an ended run whose stored segments cannot be read | `503` |

`GET` on the same path lists the segments (viewer); both routes accept a run's reference key. Where the installed segments may be incomplete it reads the store and merges: on an HA follower (which never installs them) always; a run marked for a re-read (its segments could not be read, or a write's outcome is unknown), running or ended, and an ended run whose segments this controller never loaded (reloaded after a restart); `503` if the store cannot be read. A segment whose receipt does not validate is listed without one. A run's read carries `telemetry_policy_segments` once its policy changed; it is a snapshot of what is installed, so where they differ `GET` is authoritative. Changes are serialized per controller (`telemetrySegmentMu`) and by the store's compare-and-append on the expected sequence; on an HA controller the append is fenced by the execution lease in the same transaction.

<a id="roadmap-telemetry-policy-segments-2026-10-23--the-boundary-and-live-classification"></a>
#### The boundary and live classification

- The boundary is the controller time taken under the runner lock before the write (`telemetrySegmentPendingAt`), truncated to the microsecond (PostgreSQL's precision, so every copy agrees). Each live publication's measurements span from the earlier of the previous classification (or the run's start) and its oldest sample's collection, to its publication (`runTelemetryIntervalPolicy`, `telemetryMeasurement.start`): a local run's collection time is on the controller's clock; a distributed sample's is mapped by its generator's qualified clock offset less the uncertainty; a distributed sample on an unqualified clock cannot be placed, so after a change its publications are unscored until the clock qualifies. Then (`telemetryIntervalPolicyLocked`):
  - wholly before a boundary: the segment in force at its start;
  - a boundary among them, or ending while a change is pending: no segment — its budget decisions are unscored with `policy:segment_boundary` (an interval without a condition ends a breach spell; installing a segment starts a new one); what no policy decides (collector and stream losses, gaps, websocket drops, transaction, broker and throttling samples) is still judged and alerted, so such an interval can still breach;
  - wholly after: the new segment, with its budgets; installing a segment also starts a new breach spell.
- A failed write clears the pending boundary and leaves the current segment in force; the intervals that ended while it was pending stay unscored.
- A retry whose first answer was lost (the write committed) installs the segment it created; a restore merges what it reads with the segments installed meanwhile. A write refused as a conflict, or failed for any reason but a lost lease, marks the run's segments for a re-read by the next publication (no segment classifies it until then), since the store may hold a segment this controller has not installed. A re-read clears that mark only if no failure was marked after it began (`telemetrySegmentsMark`): a later failure's segment may be missing from what it read.
- `telemetry_status.policy_segment` records the classifying segment on the live status and each archived interval. `LiveMetrics.telemetry_policy` is the classifying segment's receipt (the segment in force, for a spanning interval); while the run's segments are unavailable it claims none (`unavailable`, reason `policy-segments-unavailable`, which the dashboard accepts for live evidence only; `apexctl` reads only manifests and archive reports, which never carry it).
- The HA checkpoint's per-collection policy hash is the segment in force from the collection (placed as above) to its acceptance; none when a change falls in that span or is pending.

<a id="roadmap-telemetry-policy-segments-2026-10-23--recovery"></a>
#### Recovery

An adopted or reloaded run reads its stored segments (`restoreTelemetryPolicySegments`, beside the telemetry checkpoint restore). Until they can be read, its intervals' budgets are unscored (`policy:segments_unavailable`, not a boundary; what no policy decides is still judged), its live evidence claims no receipt (the segment in force is unknown) and changes are refused with `503`; the read is retried every 5 seconds from publication and by each change request. Finalization reads them from the store into its snapshot of the run (the record, or a copy of the live run), serialized with changes (`telemetrySegmentMu`), so a change admitted before the run ended is always in what it records; while they cannot be read, the finalization step fails (`errTelemetrySegmentsUnreadable`) and is retried, since coverage is written once. A stored segment whose receipt does not validate is not retried, since no re-read repairs it: live, the run stays unscored (no segment can classify it), its valid segments are installed and listed beside the invalid one (without a receipt), and a change is refused with `409`; a clean re-read (only on a re-adoption or reload, since retries and change requests skip it) clears it; at finalize it is recorded as a segment without a receipt (its coverage `unavailable: immutable-policy-invalid`, the report's policy "receipt invalid"), so the run still publishes, and an ended run's list carries it the same way.

<a id="roadmap-telemetry-policy-segments-2026-10-23--archive-report-comparison-and-re-evaluation"></a>
#### Archive, report, comparison and re-evaluation

- The manifest lists the segments (`telemetry_policy_segments`, when more than one) and the coverage record's segments carry their sequence and each one's healthy, degraded and breached time from the intervals it classified. An interval archived without a segment (by an older controller) counts for the single segment of a run whose policy never changed, or for the segment whose span holds it; one this controller left without a segment on purpose (its `unscored` names a policy reason (`policy:segment_boundary` or `policy:segments_unavailable`)) counts for no segment of a changed run, only in the run's totals (so a loss or sample condition in it can make the totals exceed the segments' sums; the HTML and PDF report say so), and re-evaluating a segment leaves it out. Run totals and the acceptance verdict cover all segments.
- The telemetry text scrubbing covers the manifest's segments (identities, requester label, receipts).
- The run report's telemetry acceptance section lists the segments in HTML, PDF and CSV (`telemetry_policy_segment` rows; a system-settings receipt reads "threshold settings" whatever its source; a segment without coverage reads n/a), and the archive report carries them (`telemetry_policy_segments`, from the masked manifest).
- A baseline comparison involving a run whose policy changed is not comparable (`policy_changed_in_run`), and each profile counts its segments (`policy_segments`).
- Re-evaluation takes an optional `segments` map (run → sequence, keys trimmed, a run named twice refused; `apexctl analysis telemetry-reevaluate --segment RUN=N`): only the intervals that segment classified are re-scored (for a run whose policy never changed, those naming segment 1 or none; an interval archived without a segment, by its span within the segment's, with the spans coverage uses: sorted, clipped to the run window and to the previous row's end, `telemetryRowSpans`), `original_policy_hash` is its receipt's, the selection is in the input fingerprint (unchanged for requests without one), and an unknown segment is unanswerable (`segment_unknown`). The intervals left out of a segment end a breach spell in its re-evaluation (the safer reading: in the run, one where a loss was still judged may have continued a spell). A whole run whose policy changed names no single original receipt: `original_policy_hash` is absent and `policy_segments` counts its receipts. Stored classifications are never rewritten. The RCA evidence digest leaves out the segments, written with the coverage after the main manifest.

<a id="roadmap-telemetry-policy-segments-2026-10-23--dashboard"></a>
#### Dashboard

- The receipt model accepts the admission boundary by default and a segment's only where a caller allows it (the live receipt and segment lists); `telemetryPolicySegmentsView` verifies a segment list (sequences 1..n, the admission source first and only first, each boundary matching its position, every digest) and refuses a malformed list whole (including a segment whose source or policy ID disagrees with its receipt: `system_settings` exactly when the receipt is the system policy); a change recorded without a receipt (`policy: null`, never the admission segment) is kept and shown as "receipt invalid".
- The run page's telemetry panel lists the segments once there are two or more (sequence, time, source label, requester, cadence, verified digest) and says an interval a change fell inside, or classified while the run's segments could not be read, belongs to no segment (its budgets not judged; what no policy decides still judged). The receipt card names a segment receipt as such, by the run's latest segment when the receipt is that segment's (a receipt the polled list has not caught up with is described without a number); admission-only views (the archive's admission receipt) still accept only an admission receipt. A running or paused run's panel offers **Change telemetry budgets** to users who can manage tests: the budget select (its empty option the threshold settings, sent as `project_default`), a whole-second cadence for a local run, a review step that fixes the submission's `request_id` and `expected_sequence` together (a change sent without an answer is kept per run, outside the control. A success or a `400`, `403`, `404` or `409` refusal ends it; any other failure keeps it, since the change may be stored; another change sent replaces it; and a review, or a confirm of the kept change itself, once the latest segment is more than one past its expected sequence drops it. Until then every review of the same policy and cadence reuses its submission, across retries, edits, Back, Cancel, another change reviewed meanwhile and a remount of the control, and a confirm from the review step sends any submission only under the same rule. Otherwise a review starts a submission from the latest segment), requests bounded at 30 seconds (a timeout keeps the change), no concurrent sends for a run (shared by every control, so a remounted one cannot send while another sends), a reload of the segments after success or a refusal and of the policy list after a refusal (after a success the list is read again when the control next opens), and a notice when a change was answered with a segment a later one has replaced since, or when a retried change (`200`) was answered but the segments could not be read to confirm it is in force. The notice is lost if the control unmounts while the change is sent; the segment list still shows the outcome at the next poll. While the run's segments are unavailable the receipt card says so and no change is offered (the server would refuse it).
- The archived run view and the run report's execution details list the segments from the archive report (or, before the archive is written, the run's own record). The list re-verifies only when its content changes, so a poll does not blank it, and it verifies the list as sent, never a JSON copy (which would turn a `-0` limit into `0`). The change control moves focus to its heading on open and back to its open button on close, announces every stored change (`The change was stored as segment N.`, or one of the notices above) through a live region present before it speaks, links the cadence error to its input, and its confirm text names a cadence change.

<a id="roadmap-telemetry-policy-segments-2026-10-23--cli"></a>
#### CLI

`apexctl analysis telemetry-policy-change --id RUN --policy ID|project_default [--interval N] [--expected-sequence N] [--request-id ID]` reads the latest segment when no sequence is given, generates and prints a request ID when none is given, and prints the new segment.

<a id="roadmap-telemetry-policy-segments-2026-10-23--validation"></a>
### Validation

- **`TestTelemetryPolicySegmentsAppendOnly`** (storage; Postgres `TestPostgresTelemetryPolicySegmentsAppendOnly`): lease fencing (and an unfenced append outside HA), the expected-sequence compare, a retried request ID, a missing test, the list, deletion with the test.
- **`TestSchemaReleaseFingerprintIsCurrent`** (v84), the deletion-graph coverage and the Postgres foreign-key specs.
- **`TestSegmentCaptureBoundary`** (`pkg/telemetrypolicy`).
- **`TestTelemetryPolicySegmentsAppendOnlyChanges`**: every refusal, a change to a policy and to the threshold settings at another cadence, the retry, a reused request ID, the stored rows, the policy in force, a distributed cadence change, a run that ended (and a retry after it).
- **`TestTelemetryPolicySegmentsClassifyLiveIntervals`**: healthy under the admission receipt, unscored across the boundary, degraded under the new budgets, unscored while a change is pending, unscored as unavailable while the segments cannot be read, and a collector loss still judged in such an interval.
- **`TestTelemetryPolicySegmentsListedFromTheStoreWhenStale`**: a run marked for a re-read lists the store's segments merged with what is installed, after it ended too; an unreadable store is an error for a running run.
- **`TestTelemetryPolicySegmentsByRefKeyAndOnAFollower`**: a change and a list by the run's reference key; an HA follower lists the stored segments.
- **`TestTelemetryPolicySegmentsFinalizeWaitsForAChangeBeingWritten`**: finalization's segment read waits for a change being written.
- **`TestTelemetryPolicySegmentAfterAnInvalidOne`**: a valid segment stored after an invalid one is listed with a receipt but never classifies.
- **`TestTelemetryPolicySegmentFailedWriteKeepsTheCurrentSegment`**, **`TestTelemetryPolicySegmentsRestoredOnAdoption`** (including an unreadable store and its retry), **`TestTelemetryPolicySegmentsCoverage`**, **`TestTelemetryPolicySegmentsInComparisonAndReport`** (the report's segments and note, rendered in the PDF and HTML), **`TestTelemetryReevaluationSelectsASegment`**, **`TestTelemetryReevaluationSegmentSelection`**, **`TestTelemetryPolicySegmentRoutes`** (statuses, audit once, list and its `503`, CI permission).
- **`TestTelemetryPolicySegmentsOfAnEndedRunAreReadFromTheStore`**: the store fallback, and the masked manifest a report carries keeps the segments.
- **`TestTelemetryPolicySegmentsRecordedAtFinalize`**: the stored segments read into a finalization snapshot, recorded in the manifest, each one's coverage (an interval left unclassified on purpose, breached within segment 2's span, counted in the run's totals only); a retried error and no coverage while they cannot be read.
- **`TestTelemetryPolicySegmentUnqualifiedDistributedRun`**: an unqualified distributed run stays on its admission segment until a change, and is unscored after it.
- **`TestTelemetryPolicySegmentConflictRereadsTheStore`**: a conflict marks the segments for a re-read, and the next publication installs the stored one.
- **`TestTelemetryPolicySegmentWithAnInvalidStoredReceipt`**: an invalid stored receipt keeps the run unscored (as unavailable) without re-reads, listed without a receipt and with no live receipt claimed, and refuses a change and the retry of the change that stored it (`409`); a clean re-read clears it; finalize records it as a segment without a receipt (manifest, coverage, report) instead of failing; the ended run's list read from the store carries it, and a failed read is an error; a stored row never duplicates a held sequence.
- **`TestTelemetryPolicySegmentRereadKeepsALaterMark`**: a failure marked while a re-read reads survives it; a clean re-read clears the mark.
- **`TestTelemetryPolicySegmentsUnreadableFailTheFinalizationStep`**: the manifest-adjunct finalization step fails with `errTelemetrySegmentsUnreadable` while the segments cannot be read, and reads them again on the retry, which succeeds.
- **`TestTelemetryReevaluationOfOneSegmentOfAChangedRun`**: a two-segment archive re-scored for segment 2 only, against its receipt, with a trimmed key; keys that trim alike refused; the whole run names no original receipt and counts two.
- **`TestTelemetryReevaluationSelectsASegment`** also: a left-out interval ends a breach spell in a segment's re-evaluation.
- **`TestSegmentCaptureBoundary`** also: `ValidateAdmission` refuses a segment receipt; **`TestTelemetryPolicySegmentsInComparisonAndReport`** also: a segment receipt is never a run's admission evidence.
- **`TestTelemetryPolicySegmentPlacementAndRecovery`**: a local sample collected before a change is spanning though the previous publication came after it; one after is the new segment's; an unqualified distributed sample is unplaceable and a qualified one is mapped by offset less uncertainty; microsecond boundaries; a replay installs; a restore keeps a segment installed meanwhile.
- **`TelemetryPolicySegments.test.tsx`** (dashboard): the segment verification, the list, the change control's request, a lost-response retry and a change polled during review keeping the submission's identity, a missing `randomUUID`, a StrictMode remount, a `409`, a segment recorded without a receipt, a source or policy ID that disagrees with its receipt (refused), a `-0` limit verified as sent and a list kept verified across an unchanged poll, a receipt the list has not caught up with named by no segment, the success announcement, focus to the heading on open and back to the open button on close, the cadence error linked to its input, the confirm text naming a cadence change, a kept change reviewed afresh once a later change is in force, a kept change not resent from the confirm step once a later change is in force, a never-sent change not confirmed once two later changes are in force, the notice when a retried change has been replaced (cleared on open) or cannot be confirmed in force, no send from a remounted control while one is sent, a refused change reviewed afresh (the confirm step left, the segments reloaded), the segments-unavailable receipt reason (refused in an admission view), the submission kept through Back and Cancel, an A→B→A edit, another change reviewed meanwhile and a remount after a send, a never-sent submission renewed after a poll, the latest segment named when a receipt is chosen again, the receipt card's wording, admission-only strictness, and the archived view.
- **`RunExecutionDetails.test.tsx`** (dashboard): the run report's execution details list a changed run's segments, from its own record before its archive.
- **`telemetryPolicySegmentsApi.test.ts`** (dashboard): the change is a `POST` of the request to the run's path; a `201` is a change made, a `200` a retry answered with it.
- **`cmd/apexctl`**: `TestTelemetryPolicyChangeSendsTheChange`, `TestTelemetryPolicyChangeRefusalsAndFlags`, `TestTelemetryReevaluateSelectsSegments`.

<a id="roadmap-telemetry-policy-segments-2026-10-23--review"></a>
### Review

Round 1 used three reviewers: the core (storage, append, classification, recovery), the consumers and CLI, and the dashboard and docs. The full pkg/api suite alongside caught three guards: the generated API contract matrix, the durable-write gauge (the append now runs in `trackDurableWrite`) and the routes' access goldens.

- **Core** (two medium, three low):
  - **Medium:** intervals were placed by publication time, but a publication's measurements start at its sample's collection, which can precede the previous publication; a change could be classified against measurements taken before it. Placement now uses the collection time, mapped through a qualified clock, and an unplaceable distributed sample after a change is unscored. The checkpoint's hash follows the same rule.
  - **Medium:** boundaries kept nanoseconds while PostgreSQL stores microseconds, so a re-read boundary differed. Both API and store truncate to the microsecond.
  - A replayed change was not installed after a lost answer; a restore could drop a change made meanwhile; a merely pending change reset the old segment's breach spell. Fixed (an unscored interval still ends a spell, as any does).
- **Consumers and CLI** (one medium, four low):
  - **Medium:** a finalize that could not read the segments wrote single-policy coverage for a changed run, never corrected. Coverage now waits for a later refresh, and re-evaluating segment 1 of an apparently unchanged run keeps only rows naming it or none.
  - Untrimmed segment keys re-evaluated the whole run; rows archived without a segment in a changed run counted for no segment; the report labelled system-settings receipts two ways and printed empty sums; the finalize and two-segment re-evaluation paths were untested. Fixed and tested.
- **Dashboard** (two medium, two low): the retry identity followed polling (a lost-response retry could append a duplicate, and a concurrent change could be silently overridden); a missing `randomUUID` locked the control; StrictMode remounts locked it in development; the receipt card misdescribed a segment receipt and admission-only views accepted one. Fixed and tested.
- **Docs:** no findings.

Round 2 used three fresh reviewers with the same split.

- **Core:** the placement arithmetic (the clock offset's sign and units, the fleet's oldest sample with the largest uncertainty), lock ordering and the replay, restore and breach paths held; an unqualified run that never changed stays scored. One low finding: a write whose reply failed after a commit (or a conflict) left this controller's segments stale, and the run classified by the old one, until an adoption. Such failures now mark the segments for an immediate re-read. One test gap closed (an unqualified distributed run before and after a change).
- **Consumers:** two medium findings against round 1's finalize fix. Finalization works on a snapshot (the record, or a copy of the live run), and the restore it called acted only on the live run, so it never read the store; and a coverage error was only logged, with nothing to retry it. Finalization now reads the store into its snapshot and fails the step (retried) while the segments cannot be read. One low finding: re-evaluation dropped intervals archived without a segment that coverage credits by span; it now uses the same rule.
- **Dashboard:** two low findings: a policy chosen again shares a receipt hash, so the card could name the earlier segment (it now names the latest); and Back or Cancel after a lost answer renewed the submission (it is now kept while the change is the same). Two record lines described the model's boundaries and the renewal rule inexactly. Fixed and tested.

Round 3 used two fresh reviewers on the round-2 fixes: the backend and the dashboard.

- **Backend** (three low, one test gap): the snapshot, unsupported stores, the failure mark and the span arithmetic held.
  - One stored receipt that does not validate (a corrupt row, or one another controller version wrote) failed finalization on every retry, so the run never published. Read failures still retry; an invalid receipt is now recorded as a segment without one.
  - Re-evaluation used each row's raw span in archive order while coverage sorts and clips them, so an edge row could be credited by one and dropped by the other. Both now use `telemetryRowSpans`.
  - A re-read could clear a failure marked while it read, leaving the run on the old segment. A mark counter now guards the clear.
  - No test showed the finalization step itself failing. Added, with tests for the two fixes above.
- **Dashboard** (three low): a submission never sent was reused after Cancel, earning a `409` for a review made after a newer segment appeared; an edit away and back after a lost answer renewed the submission and could append a duplicate; the test inventory lagged. The control now reuses only a sent submission, edits no longer clear it, and the record lists the tests.

Round 4 used two fresh reviewers on the round-3 fixes.

- **Dashboard** (four low): reviewing another change after a lost answer replaced the unanswered submission, so returning to the first change could append it twice; the submission was lost when the control remounted (the run's live metrics dropping out), and the control had no `key` for its run; a comment still said edits renew the submission; the request-ID stub returned one value, so tests could tell submissions apart only by sequence. An unanswered change is now kept per run outside the control until answered, the control is keyed by run, and the stub returns distinct IDs; two tests cover the review-meanwhile and remount cases.
- **Backend** (six low): the round-3 fixes held (failed reads still retry the step; receipt-less segments are safe in the report, scrub, comparison and re-evaluation; the shared spans leave coverage unchanged; the mark counter is read and compared under the lock).
  - An invalid stored receipt was re-read every 5 seconds for the rest of the run, and changes were refused with a transient-sounding `503`. It is now recorded (`telemetrySegmentsInvalid`), never re-read, and changes are refused with `409`.
  - The dashboard refused a whole segment list holding a receipt-less segment, while the report listed it. The model now keeps it and shows "receipt invalid"; the report labels it the same way.
  - An ended run's list dropped every stored segment when one was invalid; it now lists them as the manifest does. Merging invalid rows could duplicate a sequence; it no longer does.
  - The shared-span fix had no regression test, a doc comment had drifted off its function, and the finalization-step test did not show the retry reading the segments. Fixed.

Round 5 used two fresh reviewers on the round-4 fixes.

- **Backend** (two medium, four low): the field's locking, the copies, the `409` placement, the de-duplication and the moved comments held.
  - **Medium:** the retry of a change whose own stored receipt no longer validates (written by another controller version) answered `500`, which the dashboard treats as no answer and retries forever. It is now `409`.
  - **Medium:** a running run with an invalid stored segment listed only its admission segment and presented the admission receipt as in force, so changes were refused for no visible reason. Its valid segments are now installed and the invalid one listed without a receipt, and while a run's segments are unavailable its live evidence claims no receipt.
  - The invalid mark was never cleared by a clean re-read; unavailable intervals were labelled as a policy change (`policy:segment_boundary`), now `policy:segments_unavailable`; an ended run's `GET` answered a failed store read with the admission segment alone, now `503`; three test-clarity points. Fixed.
- **Dashboard** (one medium, five low): the model's refusal rules, every consumer of a receipt-less segment and the new tests held.
  - **Medium:** a kept change was reused as a retry however old: after another change, choosing the same policy again replayed the old request and the server answered success while the other change stayed in force. It is now reused only while the latest segment could be the one it made.
  - A late answer could delete a newer change's entry, and a remounted control could send while another sent (the guard was per control); a `409` after unmount left the entry; `400`, `403` and `404`, definite refusals, kept it; the record overstated how long the entry lives. An answer now ends only its own entry, the send guard is per run and shared, refusals end it before the mounted check, and the record says so.
  - A receipt-less row with a source the dashboard does not know (a corrupt row) still refuses the whole list. Accepted: it needs a corrupt source as well as a corrupt receipt, and the report still lists the segments.
  - The kept-change map is module state shared by tests on one run ID; each test ends with its change answered. Accepted, noted here.

Round 6 used two fresh reviewers on the round-5 fixes.

- **Backend** (three medium, three low): every classifying path goes through the unavailable check first, so a valid segment installed after an invalid one never classifies; the replay `409`, the `GET` `503` and the regenerated contracts held.
  - **Medium:** an ended run marked for a re-read (a write's outcome unknown when it ended) listed fewer segments than its manifest. `GET` now reads the store for any run marked for a re-read and merges; the run read remains a snapshot of what is installed, and the record says `GET` is authoritative.
  - **Medium:** while a run's segments were unavailable, its live evidence said no receipt was ever retained. It now carries `policy-segments-unavailable`, which the dashboard's live views accept.
  - **Medium:** an unscored interval replaced the whole diagnostic, so losses, gaps and sample alerts no policy decides were never judged or alerted, for the rest of a run with an invalid receipt. Only the budget decisions are now unscored; the rest is judged as before.
  - A running run whose segment read failed listed its admission segment alone (now `503`); the record's live-evidence line and re-read clause were inexact; three test gaps (a valid segment after an invalid one, the publication's unavailable label, the `GET` `503`). Fixed.
- **Dashboard** (one medium, four low):
  - **Medium:** a kept change could be resent from the confirm step after another change, bypassing the review's rule, and a polling lag could let a review reuse it: the server answered success with the old segment while the other change stayed in force. The confirm step now applies the same rule, and a success whose segment a later one has replaced says so.
  - Requests that never settled held a run's shared send guard (now bounded at 30 seconds); the receipt card said no receipt was retained while segments were unavailable (fixed with the backend's reason); the record overstated how the kept change ends; a refusal other than `409` left the policy list stale. Fixed.

Round 7 used two fresh reviewers on the round-6 fixes.

- **Backend** (one medium, five low): none of the conditions still judged in an unclassified interval reads the run's policy (transaction samples use the scenario goal); the signature, the breach spell, the checkpoint, the evidence reason and the merged list held.
  - **Medium:** coverage and re-evaluation attributed every archived interval without a segment by its span, the rule meant for intervals archived before segments existed. Intervals this controller left without a segment on purpose (a change fell inside, or the segments could not be read), which can now be breached, were credited to a segment and re-scored against its receipt. Such intervals (their `unscored` names a policy reason (`policy:segment_boundary` or `policy:segments_unavailable`)) now belong to no segment of a changed run; coverage reads the reason from the archive.
  - The report's and the panel's wording said such an interval is unknown or unscored, and why the segments' sums can fall short of the run's totals went unsaid; the record's breach-spell clause was wrong for an interval with a condition; a test's lag was under the default budget, so it did not show budgets skipped; an invalid-receipt run re-read the store on every `GET`. Fixed. A suggested `apexctl` test is moot: `apexctl` reads only manifests and archive reports, so it no longer accepts the live-only reason.
- **Dashboard** (five low): the timeout, caller aborts, unexpected answers and unmount handling held.
  - The confirm step's rule applied only to the kept change itself, so another control replacing it left the old request sendable; it now applies to any submission. A retried change (`200`) whose follow-up read failed reported plain success; it now says the change could not be confirmed in force. The live-only reason was accepted in admission views; it no longer is. A refusal test did not check the confirm step was left or the segments reloaded, nothing checked the notice clears, and the record described the policy list's reload inexactly. Fixed.
  - The notice is lost if the control unmounts while its change is sent. Accepted and recorded: the segment list shows the outcome at the next poll.

Round 8 used two fresh reviewers on the round-7 fixes.

- **Backend** (four low): archived intervals keep their `unscored` reasons on every path that writes them (local, distributed through the controller's status, none after an adoption, which counts as unknown everywhere); a single-segment run rightly credits its unclassified intervals to segment 1; totals and the verdict cover every row. A doc comment sat on the wrong function; the PDF lacked the HTML's note on the segments' sums (both now render one shared note, `telemetryAcceptanceSegmentsNote`); three texts said only losses are judged in an unclassified interval, when sample conditions are too; the archive decode of `unscored` and the invalid-receipt `GET` skip were untested. Fixed; the reason match is now exact (the two policy reasons) rather than a prefix.
- **Dashboard** (three low, one wording): the response envelope, the confirm guard, the notices, the live-only reason and the wording held. The notice-clears test could not fail; nothing tested the guard on a submission other than the kept change; the `200`/`201` mapping was untested; one record clause overstated when a confirm drops the kept change. Fixed, with an API test (`telemetryPolicySegmentsApi.test.ts`).

Round 9 used two fresh reviewers on the round-8 fixes.

- **Backend** (two low): the exact reason match, the shared note (the view is never serialized; DOCX renders through HTML; CSV deliberately carries no note) and the tests held. The new constant had displaced a function's doc comment, and nothing rendered the note: the report test now renders the PDF lines and the HTML report for a changed run, and a steady run's PDF for neither.
- **Dashboard** (four low): the round-8 tests can fail as claimed, and the component met every goal on a full read. Nothing checked the request's method, path and body; the per-run state was not reset between tests (it now lives in `telemetryBudgetChangeState.ts`, cleared after each test); one mock had the wrong shape; the panel omitted the unreadable-segments case the report names. Fixed.

Round 10 used one fresh reviewer on the round-9 delta. The HTML assertion is real (the engineer profile renders the section, the template escapes as `html.EscapeString` does, and an empty manifest renders no note), the PDF checks and the test isolation held under shuffled order. Two low findings: the panel's wording test was loosened past the new clause, and the HTML check had no negative case or table check. Both tightened; a stray blank line removed.

<a id="roadmap-telemetry-policy-segments-2026-10-23--whole-branch-review"></a>
### Whole-branch review

Once round 11 found nothing, three fresh reviewers read the whole branch (this slice and the [publication alignment](#roadmap-telemetry-publication-alignment-2026-10-23) commit before it): storage and the live path, the archive consumers and CLI, and the dashboard.

- **Storage and live path** (two medium, two low): the store's transaction, lease fencing, locking, pending boundary, failure marking and the publication goroutines held.
  - **Medium:** a publication slower than a quarter of the cadence made the next due collection tick look too soon (the skip measured from the previous publication's end), so it waited for the watchdog, late and unaligned. The skip now measures from the publication's start (recorded with the alignment slice).
  - **Medium:** an HA follower, which serves reads and never installs a run's segments, answered `GET` for a running run with its admission segment alone, so a change built on it was refused every time. A follower now always reads the store (after refreshing its runs).
  - A change admitted just before the run ended could commit after finalization read the segments, so the write-once manifest recorded one policy; finalization's read is now serialized with changes. The segment routes did not resolve a run's reference key. Fixed and tested.
- **Archive consumers and CLI** (four low, one wording): coverage and re-evaluation attribution, masking, comparison, re-evaluation's fingerprints and older archives held.
  - The RCA evidence digest included the segments, written after the main manifest, so a later refresh made a completed analysis stale. A segment's re-evaluation joined breach spells across intervals it left out. A whole changed run's re-evaluation named the admission receipt as its original. A segment receipt was accepted as a run's admission evidence by the backend and `apexctl` (the dashboard refused it). The guide said a segment is re-scored against its receipt. All fixed, with tests.
- **Dashboard** (six low): digest verification, admission-only views, the change control's races and RBAC held.
  - The receipt card could name a stale segment while the polled list lagged the live receipt; the segment table blanked on every poll while it re-verified; the run report's execution details never showed the segments; the model did not check a segment's source and policy ID against its receipt; two texts misstated what an interval a change fell inside is, and the confirm text omitted a cadence change; the control lost focus on open and close, announced no plain success, and did not link its cadence error. All fixed, with tests.

The fixes were reviewed again by two fresh reviewers.

- **Dashboard** (two medium, five low): the receipt card's naming, the model's source and policy-ID rule (it matches what the controller sends: threshold-settings segments carry no policy ID) and the focus effect held.
  - **Medium:** keying the list's verification by its JSON verified a JSON copy, which turns a `-0` limit (which Go accepts and encodes) into `0`, so a valid list could be refused. The list itself is now verified; the content key only decides when.
  - **Medium:** the notice's live region was hidden while empty and present only while the control was closed, so a plain success could go unannounced. It is now always present and visible.
  - The run report's execution details had no list before an archive is written; a comment misplaced the receipt; several of the round's fixes had no test; the test fixture for a threshold-settings segment carried a policy ID the controller never sends; two texts named the unclassified interval differently. Fixed, with tests.
- **Backend** (three low, three wording): the cadence stamp, the follower and reference-key reads, the finalization lock (no deadlock: its only caller holds neither lock), the RCA digest, the spell breaks, the admission-only validation (no caller that legitimately sees a segment receipt now refuses it) and the new tests held.
  - The RCA digest fix had no test (one now records segments and checks the digest); a newly elected leader that had not yet adopted its runs listed one segment (the follower check now uses `isExecutionLeader`, true only once adoption is done); coverage's single-segment path still accepted any receipt as the admission one (that path now refuses any non-admission receipt).
  - The watchdog's timing, the late publication's delay and the spell-break clause were worded too strongly. Fixed.

A last fresh reviewer read those fixes and found five low items. All are fixed; the code items have tests.

- The record's formatting and one count were wrong.
- No test pinned the live region staying one node, present open and closed.
- Coverage's single-segment branch for a valid admission receipt was dead (a valid one takes the multi-segment path) and untested. It is removed, and a test shows an admitted run naming its receipt and a segment receipt there counted as invalid.
- The leader check was untested. A test shows a leader still adopting its runs reading the store.
- The list's ref was written during render. It is now set in an effect before the verification.

Two more fresh reviewers then read those last changes and found only wording and test-guard nits: the admitted-run case now also pins one segment and a non-empty hash, and the list above has its count, style and claims corrected. Fixed.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-policy-selection-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-policy-selection-2026-10-22"></a>

## Telemetry acceptance policy selection — 2026-10-22


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) adds explicit non-default policy selection. TEL-3 asks that the CLI support policy selection; this slice delivers it for `apexctl ci run` and the API. Plain `apexctl run` remains open.

Before this slice, every run was judged by its project's default telemetry acceptance policy, pinned at Start. A pipeline could not ask for a different gate for one run without changing the project's default for every run.

<a id="roadmap-telemetry-policy-selection-2026-10-22--contract"></a>
### Contract

<a id="roadmap-telemetry-policy-selection-2026-10-22--choosing"></a>
#### Choosing

- `telemetry_acceptance_policy_id` is accepted on:
  - `POST /api/v1/tests` (`CreateTestRequest`), trimmed before the keyed-create digest;
  - `POST /api/v1/ci/runs` (`ciRunCreateRequest`);
  - `POST /api/v1/remotes/{id}/runs`, which forwards it to the remote controller.
- `apexctl ci run --telemetry-policy <id>` sends it, in controller mode only, and so does the composite action input `telemetry-policy`. It is omitted when unset, and it is part of `apexctl`'s derived idempotency key when set.
- `project_default` names the project's default explicitly. It matters for a revised copy, where it drops the source run's choice. Elsewhere it is the same as naming none: CI and `apexctl` treat it as unset. A keyed retry of `POST /api/v1/tests` must still repeat its spelling, because on a revised copy the two spellings differ.

<a id="roadmap-telemetry-policy-selection-2026-10-22--at-creation"></a>
#### At creation

`validateRunTelemetryAcceptancePolicy` requires one of the run's own project's live, valid acceptance policies, at most 128 bytes. Lookups never leave the run's project, so another project's policy is refused exactly like a missing one. Failures are answered as:

| Choice | Answer |
| --- | --- |
| A policy the project lacks | `400` |
| An invalid policy (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

The choice is persisted with the run on the tests table (`telemetry_acceptance_policy_id`, schema v74, SQLite and PostgreSQL). The release was renumbered v80 when the branch was merged with `origin/main`, whose own v73–v79 came first. An older binary refuses the v80 database.

A revised copy of a queued run keeps its source's choice unless it names another, or `project_default`. An inherited choice that is no longer usable is refused with a message that names the source run's choice and the way out.

<a id="roadmap-telemetry-policy-selection-2026-10-22--ci"></a>
#### CI

- **Permission.** A new CI run may choose only where the project's GitHub Actions policy sets `allow_telemetry_policy_selection`. That is an administrator's decision, like `validity_gate`, and it is off by default. Otherwise the choice is refused (`403`), so a pipeline cannot exchange a `fail_ci` default for a weaker policy. `project_default` needs no permission.
- **Early check.** A new run's choice is checked before any GitHub check or run is written.
- **Idempotency.** The choice is part of the run's idempotency metadata, so a retry under the same key that names another policy conflicts. An exact retry of an accepted run returns that run even after its choice was disallowed or deleted, as an accepted run keeps its validity gate.
- **Inconclusive mapping.** `ciTelemetryInconclusiveRequirement` judges by the chosen policy when there is one.
- **Echo.** The CI responses echo the choice (`telemetry_acceptance_policy_id`). An older controller ignores the field and would judge the run by the default. So when the create response lacks the echo, `apexctl` stops the accepted run and exits with a configuration error. It also refuses a final response that lacks it.
- **Audit.** The `ci.run.create` audit event records the choice.

<a id="roadmap-telemetry-policy-selection-2026-10-22--remote-runs"></a>
#### Remote runs

A remote controller that does not echo the forwarded choice would judge the run by its project's default. Its run is deleted before it starts, and the create is refused (`409`).

<a id="roadmap-telemetry-policy-selection-2026-10-22--at-start"></a>
#### At Start

`resolveRunPolicyPins(projectID, selected)` pinned the chosen policy's current revision. Since the `origin/main` merge, `resolveRunPolicyPinsForRun(projectID, selections, selectedAcceptance)` does this alongside main's browser and journey selections. It pins the chosen revision with source `run_selection` (`runPolicySourceSelected`), and freezes its definition beside the pin in place of the default. The other policy kinds keep their defaults.

A chosen policy that is gone or invalid by Start refuses Start (`errRunPolicyConfiguration`), as an invalid default does, rather than falling back to the default. A direct Start leaves the run queued.

<a id="roadmap-telemetry-policy-selection-2026-10-22--shown"></a>
#### Shown

- The verdict's policy pin carries `source: run_selection` in the CI response, the run manifest and the report JSON.
- The GitHub check says "(chosen by the run)".
- The `apexctl` job summary says "policy chosen by the run".
- The HTML/PDF reports and the dashboard show the policy's name and revision only.

<a id="roadmap-telemetry-policy-selection-2026-10-22--validation"></a>
### Validation

- **`TestRunChosenTelemetryAcceptancePolicyIsPinnedAtStart`**:
  - another project's policy refused like a missing one;
  - an over-long ID;
  - a trimmed choice persisted, and kept by a runner reloaded from the store;
  - a revised copy keeping it, `project_default` dropping it, and another choice replacing it;
  - Start pinning the chosen policy (`run_selection`, its `fail_ci` definition) and not the default.
- **`TestRunChosenTelemetryAcceptancePolicyGoneRefusesStart`**: a policy deleted after creation refuses Start and leaves the run queued, and a revised copy inheriting it is refused with the source run's choice (`doomed-gate`) named.
- **`TestCreateTestRouteChosenTelemetryAcceptancePolicy`**: `POST /api/v1/tests`, with a keyed retry spelling the choice with spaces replaying the run, a keyed retry naming another policy conflicting, and an invalid policy answered `409`.
- **`TestChosenTelemetryAcceptancePolicyStoreOutageIs503`**: a policy store outage answered `503` on `POST /api/v1/tests` and `POST /api/v1/ci/runs`.
- **`TestCITelemetryInconclusiveRequirementFollowsTheChosenPolicy`**: the mapping requirement under each combination of default and choice, an unknown choice, and an explicit mapping.
- **`TestCIRunCarriesTheChosenTelemetryAcceptancePolicy`**, on `POST /api/v1/ci/runs`:
  - refusal while selection is not allowed, and `project_default` accepted there with nothing echoed;
  - the choice reaching the run and echoed;
  - a retry naming another policy conflicting;
  - an unknown choice refused (`400`) with and without a mapping;
  - an exact retry returning the run after selection was turned off and the policy deleted.
- **`TestCIGitHubCheckNamesAChosenPolicy`** and **`TestTelemetryVerdictModeLabelNamesAChosenPolicy`**: the GitHub check and the `apexctl` summary label a chosen policy, and only a chosen one.
- **`TestRemoteRunNeedsTheChosenPolicyConfirmed`**: a remote run whose controller does not echo the choice is deleted and refused (`409`); one that does is created.
- **`TestParseCIArgsTelemetryPolicy`**, **`TestDerivedCIIdempotencyKeySeparatesGateConfiguration`** and **`TestControllerCIRunNeedsTheChosenPolicyConfirmed`**:
  - the `apexctl` flag, with `project_default` as no choice;
  - the derived key, pinned for an unchosen request to the value before this slice, and changed by a choice;
  - an unconfirmed create stopped without polling and reported as a configuration error.
- **`TestSchemaReleaseFingerprintIsCurrent`**: records schema v74 (v80 after the `origin/main` merge; the schema is now v81). The PostgreSQL storage integration suite passed on v74 with `go test -tags postgres_integration ./pkg/storage` and `APEXLOAD_TEST_POSTGRES_URL` set. It is run again on the merged v81 schema; the merge records that run in the scenario policy slice's README.

<a id="roadmap-telemetry-policy-selection-2026-10-22--review"></a>
### Review

Round 1 used two reviewers, one on the backend and one on contracts:
- A CI caller could exchange a `fail_ci` default for an advisory policy. CI selection is now an administrator's opt-in.
- An older controller silently dropped the choice. The choice is now echoed and checked.
- Errors:
  - a revised copy could not drop an inherited choice;
  - a store failure looked like a refusal;
  - the CI choice was checked after GitHub check work.
- Wording and tests:
  - the derived key ignored the choice, and the keyed digest was taken before trimming;
  - the wording was stale and the docs overclaimed;
  - there were test gaps.

Round 2, the same reviewers:
- **Retries.** An exact CI retry was refused once the choice was disallowed or deleted. The check now applies only to a new run.
- **`project_default` on CI** needed permission and ended in an unconfirmed-choice error. It is now no choice.
- **Late detection.** `apexctl` noticed a missing echo only after the whole load. It now stops the run at create.
- **Remote runs** dropped the choice silently on an older remote. Such a run is now deleted and refused.
- **Smaller items:**
  - the audit event lacked the choice;
  - the dashboard's GitHub Actions policy type lacked the flag, so its round-trip test missed it;
  - the README claimed an untested capacity-queue outcome;
  - evidence and assertion gaps.
- **Not changed.** A keyed `POST /api/v1/tests` retry must repeat the spelling of `project_default`, because a revised copy gives it a different meaning from an empty choice.

<a id="roadmap-telemetry-policy-selection-2026-10-22--remaining"></a>
### Remaining

- **Dashboard.** There is no dashboard control for choosing a policy for a run, and no revise-modal notice of an inherited choice.
- **Entry points.** Plain `apexctl run`, scheduled runs and execution-plan runs use the project's default.
- **Telemetry budget policy.** A run cannot choose a telemetry budget policy (`telemetry_policy`).
- **Library bundles.** A run record carrying a choice changes the library result's content hash, so an older importer refuses it, as with any new run-record field.
- **Rest of TEL-B.** In-run policy segments and the rest of the TEL-B consumer matrix.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-publication-alignment-2026-10-23/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-publication-alignment-2026-10-23"></a>

## Telemetry publication alignment — 2026-10-23


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) resolves the generator stage-budget mismatch the specification requires before TEL-C, from measured collection and publication scheduling. The decision is **scheduling alignment**, not a budget revision: the receipt, its cadence and the 500 ms default stage budgets are unchanged, and no archived run is reinterpreted.

<a id="roadmap-telemetry-publication-alignment-2026-10-23--what-was-measured"></a>
### What was measured

The 20-second reference workload (`TestTelemetryReferenceWorkload`, `APEXLOAD_TELEMETRY_REFERENCE_SECONDS=20`: two generators over in-memory gRPC, 2,000 accepted observations a second, a headless browser on the live page, darwin-arm64 with 10 CPUs) was run on `main` (`33125479c`), after each change, and on the final code after review (the last column).

| Stage, ms (P50 / P95 / P99) | Before | Generators aligned | Gate aligned too (final code) |
| --- | --- | --- | --- |
| Generator processing | 502 / 503 / 503 | 14 / 20 / 20 | 12 / 19 / 21 |
| Transport | 6 / 8 / 9 | 4 / 6 / 8 | 4 / 7 / 11 |
| Controller fan-out | 4 / 974 / 1,001 | 5 / 1,003 / 1,004 | 2 / 588 / 1,001 |
| Sample age (collection to controller publication) | 513 / 1,512 / 1,512 | 1,019 / 1,023 / 1,024 | 19 / 237 / 494 |
| Browser render | 124 / 174 / 174 | 97 / 158 / 207 | 93 / 181 / 188 |

| Coverage under the default budgets | Before | Generators aligned | Gate aligned too (final code) |
| --- | --- | --- | --- |
| Healthy / degraded / breached / unknown | 0 / 13.0 s / 6.0 s / 2.4 s | 8.5 s / 8.0 s / 0 / 4.9 s | 19.5 s / 0 / 0 / 2.0 s |

The independent end-to-end criterion (capture to visible render at most 2,000 ms) is reported separately from stage health. The harness's bound on it, the largest sample age plus browser render, was 1,687 ms, 1,230 ms and 682 ms: within the target in all three runs. That sum leaves out delivery from the controller's publication to the browser's receipt, so it does not bound capture to visible render.

Every run accounted for all served requests but at most two in flight at the stop (the harness bound is one per VUser, 20 here).

<a id="roadmap-telemetry-publication-alignment-2026-10-23--cadence-and-phase"></a>
#### Cadence and phase

- **Generator collection.** The engine's metrics collector ticks every second, from when `MultiGroupEngine.RunAt` starts it (after its runtime preflight). That is before the coordinated start barrier: it runs during the commit lead.
- **Generator publication.** The publisher ran its own one-second ticker from the start barrier. It published the latest snapshot, so the published age (`GeneratorProcessingLagMs = now - snapshot.Timestamp`) was the two tickers' phase offset, the commit lead modulo one second: 502 ms here, P50 to P99 within 0.3 ms (steady-state samples within 1.5 ms). In the batch trace, publication minus collection had a median of 502 ms.
- **Actual work.** After alignment, collection to publication is 7–26 ms (median 12–14 ms; 8–21 ms in the final run): draining the run's results, computing the snapshot, resources and the publication's encoding.
- **Local controller.** The controller's broadcaster had the same two-ticker shape: its cadence ticker against the engine's collector.
- **Controller gate.** The distributed gate published at once only when a full second had passed since its last publication, and otherwise scheduled a flush a second after it. Of two windows arriving within a second of a publication, the first was published and the second held for about a second. Before alignment no steady-state publication was held; windows arrived together twice, merged into the next publication (the two 1,512 ms sample ages). Aligned, they arrive together, a few milliseconds apart, so the second of each pair was held: 9 steady-state publications waited about 1,000 ms, and since a publication's sample age follows its older window, the sample-age median rose from 513 ms to 1,019 ms while its tail fell.
- **After the gate change** steady-state fan-out is 1–3 ms. The tail is two publications present in every run: the pre-start lifecycle window (about 1,000 ms), and the stop publication (450 ms here). The two stop windows arrived about 550 ms after the previous publication; the first, a partial set, scheduled the one-second flush, and the second completed the set before the 750 ms spacing, so the set waited for that flush. An earlier run of the gate change, before the review fixed its partial-set spacing, measured sample age 18 / 131 / 248 ms.

<a id="roadmap-telemetry-publication-alignment-2026-10-23--contract"></a>
### Contract

- **`MetricsCollector.CollectionTicked`** (`pkg/engine`) returns a channel closed after the next collection tick, once that tick's snapshot (if it stored one) is the latest; every waiter wakes. `MultiGroupEngine.CollectionTicked` exposes it.
- **`engine.PublishOnCollection`** publishes once per cadence, each time right after a collection tick. A tick sooner than the cadence less a quarter (at most 250 ms) after the previous publication started is skipped (measured from its start, so a slow publication does not push the next one to the watchdog), so a longer local cadence publishes on every n-th collection. Without ticks, a watchdog publishes a cadence and a half after the previous publication ended.
- **Generator** (`runExecutionPublisher`): follows its group's engine. Groups started by one commit enter `RunAt` together, so their collectors share a phase within milliseconds; a source plan shares one engine.
- **Local controller** (`broadcastMetrics`): follows the run's engine at the receipt's cadence, still publishing only new snapshots.
- **Distributed gate** (`shouldPublishDistributedMetricsBatch`): publishes when every connected generator has stored a window since the gate's last publication (tracked by accepted revision: `remoteMetricsStoredRevisionByTarget`, `remoteMetricsGateRevision`) and at least 750 ms have passed. Otherwise it schedules one flush: for a complete set, at the 750 ms spacing; for a partial set, 150 ms later to collect the missing windows, but never sooner than a second after the last publication. Generators out of phase (older generators, or a replacement started on a later barrier) therefore still publish once a second. A window that completes the set once it is due publishes at once, and the scheduled flush then finds itself superseded, as before; a set completed before the 750 ms spacing while a partial-set flush is pending waits for that flush (at most 250 ms longer).
  - A generator whose stored window is terminal (its workload ended) does not hold back a set.
  - A flush that ends without publishing (its consumer ended, or its fence went stale after a topology change) releases its reservation.
  - A partial set is published once 1.3 s have passed since the last publication, whatever flush is pending, so a lost flush can never stall publication.
  - The gate's revision state is reset wherever the accepted revision is (a new remote lifecycle, a failed start, an adoption).
- **Unchanged:** the receipt (`ExpectedControllerPublicationIntervalMs`, `CadenceScope`), budgets, classification, coverage and archive formats. Runs archived before keep their measured stages.

<a id="roadmap-telemetry-publication-alignment-2026-10-23--not-covered"></a>
### Not covered

- The reference workload ran 20 seconds on one host, not the declared 60-second, 4 vCPU / 16 GiB release lane; that is TEL-C's.
- The decision rests on one 20-second run per change on one host. Groups of one assignment share a collector phase only as closely as their runtime preflights take equal time, and groups started by different commits (a later scale-out into a new group) keep their own phase; the elected group's collector sets the publication.
- Older generators keep their own ticker until upgraded, and their windows are not aligned; the gate keeps them at one publication a second but was not measured with them.
- **Comparisons across this change.** The receipt and its hash are unchanged, so a baseline comparison or a trend treats runs before and after this change as classified alike. Their generator-processing and sample-age stages differ by the removed phase offset (about 500 ms here): a real change of the pipeline that measured them, not of the application. The receipt's cadence scope still reads `local-controller-ticker` for local runs, a name for the cadence, which the controller keeps.

<a id="roadmap-telemetry-publication-alignment-2026-10-23--validation"></a>
### Validation

- **`TestPublishOnCollectionFollowsTicksAtTheCadence`**: a tick publishes at once, a tick too soon after the previous publication is skipped, a due one is published.
- **`TestPublishOnCollectionWatchdogWithoutTicks`** and **`TestPublishOnCollectionStops`**.
- **`TestPublishOnCollectionPublishesFreshSnapshots`**: with a real collector and the publisher started half an interval (200 ms) after it, every published snapshot is at most a quarter interval old.
- **`TestCollectionTickedWakesEveryWaiterAfterTheSnapshot`**: one tick wakes every waiter after the snapshot is stored, and a closed channel is not handed out again.
- **`TestDistributedPublicationGateKeepsTheCadenceForOutOfPhaseGenerators`**: two generators half a second apart publish once a second over two cycles.
- **`TestDistributedPublicationGateNeverStalls`**: a flush whose consumer ended releases its reservation; a partial set behind a lost flush is held only until overdue; a generator whose workload ended does not hold back a set.
- **`TestDistributedMetricsTopologyRebasesOwnedSurvivingStream`** (updated): a stale pre-topology flush never consumes a newer reservation, and one that still holds its reservation releases it.
- **`TestDistributedPublicationGatePublishesOnceEveryGeneratorHasANewWindow`**: with two generators, the first window publishes at once; one generator's window waits the coalescing window and its sibling publishes at once, superseding the flush; a complete set too soon after a publication waits for the spacing, with one flush pending; once due it publishes.
- The three reference workload runs above, and the existing engine, generator and distributed-metrics suites.

<a id="roadmap-telemetry-publication-alignment-2026-10-23--review"></a>
### Review

Round 1 used two reviewers, one on the scheduling code and one on the record's measurements and claims.

- **Scheduling code.** `PublishOnCollection`, the collector's tick channel, the generator's final publication order and the local broadcaster were sound. The gate had four findings:
  - **High:** a flush that returned early (stale fence after an evacuation, or an ended consumer) kept its reservation; under the completeness rule the gate could then stop publishing for the rest of the run. Every early return now releases it, and an overdue partial set publishes regardless.
  - **Medium:** a generator whose workload ended never stored another window, so every later publication waited out the coalescing flush. Terminal targets no longer count.
  - **Medium:** generators out of phase made the gate publish every 750 ms, faster than the receipt's one second, which would also have shortened sample-counted stops and fed the goal controller partial-fleet samples more often. A partial set now waits the full second.
  - **Low:** the gate's revision state was not reset with the accepted revision.
- **Record.** Every number matched the harness output. Findings: the separate end-to-end criterion was not reported; the gate explanation contradicted the publication trace (only the second of two windows arriving together was held); the fan-out tail and unknown time were not explained or shown; comparisons across the change, the cadence scope's name and the out-of-phase cadence were unstated limits; and the collector starts after its runtime preflight, not on entry. All are corrected above.

Round 2 used two fresh reviewers with the same split.

- **Gate fixes.** The fixes held: a partial set publishes no sooner than a second, an overdue publication cannot repeat, every early return releases only its own reservation, terminal and never-stored targets cannot stall the gate, and the resets cover every site that clears the gate's state. One finding: an existing test asserted that a stale-fence flush keeps its reservation, the behaviour that could stall the gate. A stale flush whose token still matches is always the reservation's holder, so releasing is correct; the test now checks that a stale flush leaves a newer reservation alone and releases its own.
- **Record.** All numbers matched. Wording: the held publications before alignment were the pre-start and stop windows (present in every run), not steady-state ones; the stop publication's 450 ms and the pending-flush case it shows were unexplained; and the phase spread was the P50–P99 range. All corrected.

Round 3 verified the rewritten test and the record's changed sentences against the final run: the record held; the test's release check could also have passed had the flush published instead, so it now also checks that the flush released without publishing. Round 4 verified that assertion: no findings.

The whole-branch review of the in-run policy segments slice (see its [record](#roadmap-telemetry-policy-segments-2026-10-23)) found one medium defect here: the skip was measured from the previous publication's end, so a publication slower than a quarter of the cadence (250 ms at one second) made the next due tick look too soon; it was skipped and the watchdog published half a cadence plus the publication's own duration late, at an unaligned phase. The skip now measures from the publication's start, with a test whose publication outlasts a quarter of the cadence (`TestPublishOnCollectionKeepsTheCadenceWhenPublishingIsSlow`).

After the merge (PR #270), CI found a regression from this slice: the controller's broadcaster starts a network latency probe, a request to the run's target, on a publication once 5 seconds have passed since the last probe, so on its first. The old one-cadence ticker never fired during a run shorter than a cadence; the first publication now follows the first collection, so such a run sent its target one request its script never made (`TestBrowserManagedAuthorReplayRunArchiveHARChain` counted the page three times instead of two, and a JMeter e2e seven requests instead of six). The probe now also waits one cadence from the broadcaster's start, as it did. A fresh reviewer confirmed the timing matches the old ticker to within one collection tick, including a cadence changed by a policy segment and the watchdog path. Accepted: a script that configures external metrics endpoints may have them read once more in a run shorter than a cadence (observability endpoints, not the script's target traffic).


<!-- Original record: docs/qa/reviews/roadmap-telemetry-sanitization-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-sanitization-2026-10-22"></a>

## Telemetry text sanitization — 2026-10-22


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) covers the text telemetry evidence carries:
- generator source and target names;
- policy names and descriptions;
- counter and stage reasons;
- exclusion and render reasons;
- the verdict text built from them.

Before this slice, that text reached run reports, comparisons, the archive manifest view, safe export and CI output as recorded. Only the durable telemetry notification envelope scrubbed it.

<a id="roadmap-telemetry-sanitization-2026-10-22--contract"></a>
### Contract

- **Scrubber** (`newTelemetryTextScrubber`). The rules are the runs' own: the scenario's and each group's, in both the run's and its admission (initial) provenance.
  - Each distinct rule is used once.
  - A rule that no longer compiles is skipped, and the others still apply. It does not count toward the engine's per-configuration limits.
  - Rules beyond those limits (128 rules, 256 KiB) go into further configurations. Each configuration applies only its patterns (`RedactionConfig.ApplyPatterns`), in order, so every rule runs before the shared scrubber, which runs once.
  - At most 1,024 distinct rules, valid or not, are read (`maxTelemetryRuleCandidates`), which bounds the compiling they cost. Runs that hold more have their rule-scrubbed text withheld (`telemetryTextWithheld`) instead of shown. So does a configuration the engine refuses.
  - Control characters and bidirectional formatting characters (including the byte-order mark) become spaces. Joiners and other format characters are kept.
- **Three forms of scrubbing:**

  | Form | Applied to | Scrubbing |
  | --- | --- | --- |
  | `text` | reasons, descriptions, verdict text | run rules, then `scrub.Value` |
  | `label` | policy names | run rules, then `scrub.ValueLabel`, which keeps a long name |
  | `scrubTelemetryIdentity` | generator targets, IDs and source names; keys (states, units, stage names, exclusion keys, segment reasons, the coverage algorithm, comparison capabilities, policy pin IDs, kinds and sources, the policy's mode and lag stages, criterion IDs) | `scrub.ValueLabel` alone; a real key passes unchanged |

  Identities never take run rules. Those are payload rules: one that masks addresses would make every generator the same label, and an archive imported from the safe export would then fail its source-coverage and validity joins.
- **Manifest projection** (`scrubTelemetryText`, applied by `maskRunArchiveManifest`). It returns a copy with these scrubbed:
  - in both provenances:
    - policy pins;
    - the acceptance policy's pin, description, mode and lag stages;
    - generator shard targets;
    - inventory targets and IDs;
    - the run's and each group's selected targets;
  - coverage source names, its algorithm, its observer-lost marker, and segment reasons and policy hashes;
  - the budget policy's keys (policy ID, hash, schema, capture boundary, algorithm, cadence scope, coverage rule, budget stages) and stage-digest keys (keys that scrub alike keep the first);
  - loss states and reasons;
  - stage names, units, availability, reasons and exclusion keys;
  - the render population's name, epoch, reference client and unqualified keys;
  - source-coverage targets and generator IDs;
  - host-network sources.

  Empty lists stay empty. That projection is the run report's manifest (JSON, HTML, PDF, DOCX, CSV), the manifest view, safe archive export and the browser diagnostics bundle. The report's host-network rows come from it too.
- **Generator lists.** `scrubTestRunManifestTargets` scrubs, as identities, every generator target and ID a test manifest lists, and its telemetry policy's keys:
  - selected, shard and inventory targets in its generator lists;
  - selected and shard targets in both configurations;
  - each group's and each script's selected targets;
  - the stored telemetry policy's availability, reason and keys (an imported archive's test manifest is stored as it came).

  The test manifest view and the safe export's test manifest (`maskTestRunManifest`) use it. So does the run report's generator-assignment appendix, in every report format (`reportTestManifest`). The report's completeness finding and the run calendar's topology (live and archived, `topologyForCalendar`) name generators the same way.
- **Verdicts.** They are judged on the recorded evidence. `scrubTelemetryVerdict` then scrubs their text: the policy pin, mode, criterion IDs and reasons, reasons and exclusions. Criterion IDs and the mode are keys clients match on; a real one passes unchanged. This applies in:
  - the report;
  - CI output (`newCIValidatedArchiveEvidence`, which feeds the response and the GitHub check summary);
  - the notification sent where the durable outbox is unavailable, whose run name is scrubbed as an identity.
- **Comparisons.**
  - A new comparison is decided on the recorded profiles. It is then shown with both runs' profiles scrubbed with both runs' rules. A policy is shared by a project's runs, so one run's rule hides the name in the other's profile too.
  - A comparison saved before this slice is shown with the shared scrubber, because its runs' rules are not at hand.
- **Unchanged.**
  - The archive keeps what was recorded.
  - The durable notification envelope keeps its own scrubbing and its identity. Its scrubbing is the full form, which can blank a long generator name.

<a id="roadmap-telemetry-sanitization-2026-10-22--validation"></a>
### Validation

- **`TestTelemetryProjectionsScrubSentinels`** plants sentinels in every field above, in both provenances, and in a generator-assignment manifest. The manifest has them in its lists, configurations, groups and scripts. The policy also has an imported lag stage that its criterion ID carries. The sentinels include credentials and tokens in addresses, a payload a run rule matches, and secrets in reasons, keys, names and the description. It asserts none appears in:
  - the built report's JSON;
  - the engineer-profile HTML and DOCX (every part of the package), rendered with the manifest `reportTestManifest` projects;
  - the report CSV;
  - the telemetry section's PDF lines and CSV;
  - the masked manifest, the masked test manifest and the report's test manifest;
  - a new comparison whose baseline lacks the rule (JSON and line);
  - a comparison saved before scrubbing (credential sentinels);
  - the CI response built by the production constructor, and its check summary.

  What survives: revisions, counts, verdicts, a long generator name (`apexload-generator-7d9f8b6c5d-x4k2p`), and two generators that a run rule matching addresses would have made alike. The masked manifest's completeness finding still says "1 of 2" were not collected. The caller's manifests still hold the recorded text.
- **`TestTelemetryTextScrubberRulesAndShape`** covers:
  - deduplication;
  - an invalid rule leaving the others in force;
  - group rules;
  - a later JSON-shaped rule still matching JSON the shared scrubber would re-encode, including past 128 rules;
  - identities ignoring rules;
  - text withheld past the bound on distinct rules;
  - control, bidirectional and byte-order characters removed, with a joiner kept;
  - a real criterion ID kept;
  - an empty source list kept empty.
- **`TestSavedBaselineComparisonServesScrubbedTelemetry`** stores a comparison with raw telemetry text and reads it through `GET /tests/{id}/baseline-comparisons/{comparison_id}`.
- **`TestTelemetryAcceptanceWebhookScrubsSentinels`** sends the non-durable `telemetry_acceptance` webhook for the sentinel run.
- **`TestRunReportRouteScrubsGeneratorTargets`** renders `GET /tests/{id}/report` with the sentinel test manifest.
- **`TestRunCalendarTopologyScrubsGeneratorTargets`** builds the calendar's live and archived topology from the sentinel generators.
- The three route tests fail when their scrubbing is removed, as do the sentinel test's lag-stage, selected-target and pin-key checks.
- **`TestTelemetryNotificationRuntimeScrubsAndPinsRecipients`** remains the durable notification's sentinel test.
- **Inventory and existing suites.** The new tests are in the telemetry acceptance inventory's sanitization row and the fault-matrix selector. The existing telemetry, archive report, sanitize, CI, baseline, comparison, masking, export, host-network and library tests pass.

<a id="roadmap-telemetry-sanitization-2026-10-22--review"></a>
### Review

Two reviewers read scratch snapshots: one on coverage and security, one on consumers and regressions.

**Round 1** found highs and mediums, all fixed:
- `InitialProvenance` kept raw policy names.
- Long identity names were blanked by the full scrubber.
- Generator targets stayed raw in other fields of the same projection.
- Saved comparisons were served unscrubbed.
- The policy description and verdict text bypassed the scrubbed copy.
- The CI wiring was untested.

Round 1 also fixed:
- empty lists becoming null;
- an invalid rule disabling every rule;
- rules running after the shared scrubber;
- the non-atomic notification being raw;
- labels from imported manifests being unscrubbed;
- group rules being ignored;
- bidirectional characters surviving;
- comparisons using one run's rules.

**Round 2** found these, all fixed:
- Run rules rewrote generator identities, collapsing them and breaking an imported archive's joins. Identities now take the credential scrubber alone.
- Generator targets stayed raw in the report's generator-assignment appendix, the test manifest view, the safe export's test manifest and the completeness finding.
- One configuration per rule meant later rules saw re-encoded text, and nothing deduplicated or bounded the rules.
- Imported controller keys (algorithm, states, stage names, units) were raw.
- All format characters were stripped, including joiners.
- Criterion IDs were scrubbed.
- The library-bundle note was wrong.
- The saved-comparison and fallback-notification fixes had no test through their routes.

**Round 3** found these, all fixed:
- Selected generator targets stayed raw: in both provenances and their groups, and in the test manifest's configurations, their groups and its scripts. Pool runs list every member there.
- Imported policy keys stayed raw: pin IDs, kinds and sources, and the definition's mode and lag stages, which criterion IDs carry.
- One 128-rule cap on the runs' union withheld text for admitted runs whose scenario and group rules together exceed it. Invalid rules also counted toward that cap.
- Segment reasons are keys, but were scrubbed as text.
- The report wiring had no route test.

**Round 4** found these, all fixed:
- The run calendar's topology served generator targets raw.
- The test of rules past 128 did not use pure JSON, so it did not prove their order.
- A configuration the engine refused would have dropped its rules silently.
- Two statements about rule limits were wrong.

**Whole-branch review** found these, all fixed:
- The budget policy's keys, stage-digest keys and segment policy hashes from an imported manifest were raw in the projection, and so were a stored test manifest's policy keys.

<a id="roadmap-telemetry-sanitization-2026-10-22--remaining"></a>
### Remaining

- **Generators named as recorded:** the archive's telemetry sidecars (`telemetry_sources`, clock observations) and resource samples, and the validity findings the report builds from them. A target that carries credentials is shown scrubbed in the projections above. It no longer matches those raw names, and two targets differing only in their credentials become alike. Rejecting credential-bearing generator targets at registration would remove both cases; that is not done here.
- **Interval summaries and live views:** their telemetry condition labels are not scrubbed.
- **Library bundles:** a safe export's manifest bytes change once with this projection. A library bundle listed before the upgrade, or by a controller on the earlier version, declares the earlier manifest hash. Importing an archive exported now for it fails its hash check until the bundle is exported again.
- **Rest of TEL-B:** in-run policy segments, explicit non-default policy selection and the rest of the TEL-B consumer matrix remain open.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-scenario-budget-2026-10-23/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-scenario-budget-2026-10-23"></a>

## Scenario telemetry budget choice — 2026-10-23


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) closes what the [run budget choice slice](#roadmap-telemetry-budget-choice-2026-10-23) left open: a scenario-level budget choice, the dashboard's budget selects and `apexctl run`. It mirrors the [scenario acceptance policy slice](#roadmap-telemetry-scenario-policy-2026-10-22), whose rules it follows unless this record says otherwise.

Before this slice, a run could choose its telemetry budget policy only on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs. A schedule or an execution-plan entry was always classified by the system threshold settings, and the dashboard's revise modal could not drop an inherited budget choice.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--contract"></a>
### Contract

<a id="roadmap-telemetry-scenario-budget-2026-10-23--scenario-choice"></a>
#### Scenario choice

- `telemetry_budget_policy_id` is a scenario field on `CreateScenarioRequest` (`POST /api/v1/scenarios`, `PUT /api/v1/scenarios/{id}`), `StoredScenario` and the authoring projection (`ScenarioAuthoringDocument`).
- It is stored on the scenarios table (`telemetry_budget_policy_id`, schema v83 `scenario-telemetry-budget-policy`, SQLite and PostgreSQL), NULL when the scenario's runs use the system threshold settings.
- The column is authored content: it is in `scenarioAuthoredColumns` (the revision triggers, which are replaced at open when their SQL changed, so upgraded databases get it) and in `scenarioRuntimeCommitFingerprint`. A change to it alone mints a new content revision.
- Empty and `project_default` both store no choice. The project's default budget policy never sets a scenario's runs' budgets, as before.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--at-save"></a>
#### At save

`storeScenarioLockedWithSources` calls `validateRunTelemetryBudgetPolicy` outside the runner lock under the same condition as the acceptance choice: when the save changes the budget choice, or keeps it while the scenario's project (derived from its scripts) differs from the one the runner has. The two choices are judged independently, so keeping one never re-judges the other.

| Choice | Answer |
| --- | --- |
| A policy the project lacks, another project's, or another kind's (an acceptance policy's ID) | `400` |
| An over-long ID | `400` |
| An invalid policy, or one whose `sustained_window_seconds` is not live classification's 15 s (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

`runPolicyErrorStatus` already maps both sentinels for create and update, so no handler changed.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--inheritance"></a>
#### Inheritance

`createTestRequestFromScenarioRevision` copies the scenario's budget choice into a request that names none (`telemetryBudgetPolicyFromScenario`), independently of the acceptance choice: a request naming one choice still inherits the other. Every entry point that runs a scenario goes through it: a direct run with `scenario_id`, a schedule occurrence and an execution-plan entry.

- A request naming another budget policy, or `project_default`, keeps its own choice. A revised copy still inherits from its source run.
- **A choice that is no longer usable** (deleted, or revised to another sustained window) refuses the run with a message naming the scenario's choice and the way out. A schedule occurrence refused this way is recorded as `failed`.
- **Policy-read outages.** A choice inherited from the scenario survives a failed read at creation, as the acceptance choice does; Start pins it or refuses. A run naming its own budget policy still gets `503`.
- **Estimates and capacity previews** skip the check through the existing `skipTelemetryPolicyCheck`, which already covered the budget choice.
- **At Start** the inherited choice is pinned (`run_selection`) and its `apexload.telemetry_policy.v2` receipt sets the run's budgets, exactly as for a run-level choice.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--not-portable"></a>
#### Not portable

- `normalizePortableScenarioRequestDefaults` clears the budget choice, so an export omits it and the library content hash ignores it.
- `importLibraryScenario` replaces any choice a bundle names: a new import uses the system threshold settings, and an import that updates a linked scenario keeps that scenario's own choice.
- `createScenarioRequestFromStored` (the rollback journal) keeps it.
- **Remote runs.** A remote run of a scenario with a budget choice must name a remote budget policy or `project_default`; otherwise it is refused (`409`, `errRemoteRunScenarioPolicy`, whose message now covers both choices).

<a id="roadmap-telemetry-scenario-budget-2026-10-23--dashboard"></a>
#### Dashboard

- `TelemetryPolicySelect` takes `kind="budget"` and lists `analysisPoliciesApi.list(projectId, 'telemetry_policy')`. Its empty option is "System threshold settings"; a project's default budget policy is never named as a default. A policy whose definition sets another `sustained_window_seconds` is shown as unusable and disabled unless it is already the choice. A choice the list no longer offers, a failed load and no project behave as for the acceptance select.
- **Scenario builder** (Advanced settings): a budget select beside the acceptance one, carried by the form state (`telemetry_budget_policy_id`) and the draft (`ScenarioDraft.telemetryBudgetPolicyId`) into the save and run payloads, never into the estimate request. A checkpoint whose budget choice is not a string is not restored. At save both choices are checked against fresh reads under the same save lock, abort and 10-second timeout; a block opens the panel and reloads only the select that blocked. A choice the scenario already stores is not judged.
- **Revise modal:** "Keep the source run's choice (name)" omits the field, "System threshold settings" sends `project_default`, and another policy sends its ID, with the same fresh-read check, busy state, stale-snapshot guard and unmount behaviour as the acceptance select.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--apexctl-run"></a>
#### `apexctl run`

`apexctl run --api URL --telemetry-budget-policy ID` sends `telemetry_budget_policy_id` and requires the create response to echo it, as `--telemetry-policy` does:
- an unconfirmed choice (no echo, another policy, or only an acceptance echo) deletes the queued run before Start and exits `2`; a delete that cannot be confirmed names the run and exits `3`;
- a controller refusal of the budget choice (`400`/`409` in the controller's wording: missing, over-long, invalid or another sustained window) exits `2`; other refusals keep exit `3`;
- `project_default` is the same as omitting the flag, and the flag needs `--api`.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--validation"></a>
### Validation

- **`TestScenarioTelemetryBudgetPolicyIsAuthoredContent`** (storage; also the `telemetry_budget_policy` case of `TestPostgresScenarioContentRevisionIntegration`): stored beside the acceptance choice and read by `GetScenario`, `ListScenarios` and the authoring snapshot; a change to it alone, including a direct column write, mints a new revision; clearing it keeps the acceptance choice; `SaveScenario` stores it.
- **`TestSchemaReleaseFingerprintIsCurrent`** records schema v83.
- **`TestScenarioTelemetryBudgetPolicyCarriesThroughEveryEntryPoint`**: `400` for missing, foreign and acceptance-kind IDs (missing and foreign with the same message) and an over-long ID; `409` for another sustained window; `project_default` stores none and spaces are trimmed; the choice in the response, the store, the authoring projection and a reloaded runner; inherited by a direct run, a plan entry and a schedule occurrence, while a run naming another policy or `project_default` keeps its own, and the acceptance choice is inherited regardless.
- **`TestScenarioTelemetryBudgetPolicyGoneRefusesInheritingRuns`**: a deleted choice refuses the inheriting run (`400`) and one revised to another window refuses it (`409`), each naming the scenario's choice; `project_default` is accepted; an unrelated edit keeps the deleted choice; a missing policy is refused at save and clearing is accepted.
- **`TestScenarioTelemetryBudgetPolicySaveStatuses`**: `409` on update; `503` on create and update during an outage; an edit and a restore keeping the choice succeed during it.
- **`TestScenarioTelemetryBudgetPolicyKeptChoiceIsJudgedAfterAProjectChange`**.
- **`TestScenarioTelemetryBudgetPolicyScheduleOutcomes`**: during an outage a governed schedule occurrence, a plan entry and a direct run are created with the choice (the occurrence's Start fails as any run's would) and a run naming its own policy gets `503`; once deleted, the occurrence is `failed`, naming the choice.
- **`TestScenarioTelemetryBudgetPolicyCreatedDuringOutageIsPinnedAtStart`**: the version 2 receipt names the scenario's policy and its limits, and the budget pin is `run_selection`.
- **`TestTelemetryBudgetPolicyIsNotJudgedByCapacityEstimates`** and **`TestScenarioTelemetryBudgetPolicyIsNotPortable`**.
- **`TestRemoteScenarioRunNeedsAnExplicitTelemetryBudgetPolicy`**: `409` through the route unless a remote policy or `project_default` is named.
- **`run_telemetry_budget_policy_test.go`** (`cmd/apexctl`): both choices sent and confirmed; an unconfirmed budget choice (no echo, another policy, an acceptance echo only) deletes the run and exits `2`; another policy is named; an undeleted run exits `3`; budget refusals in the controller's four wordings exit `2`, an acceptance refusal with only a budget choice and an unrelated refusal exit `3`; no choice sends none; flag parsing.
- **Dashboard:** `TelemetryPolicySelect.test.tsx` (budget options and wording, the unusable window, the issue helpers), `TestHub.test.tsx` (save, a policy revised since the select loaded to another window blocked by the fresh read and the budget select reloaded, estimates omitting the field), `CreateTestModal.test.tsx` (keep omits the field, `project_default` and another policy are sent; a policy usable when the select loaded but revised to another window is refused by the create's fresh read, and a kept choice the project no longer has and an unusable one are refused), `scenarioAssembly.test.ts` and `scenarioAuthoringDraft.test.ts`.
- A negative control (removing the inheritance line) failed four of the API tests.

<a id="roadmap-telemetry-scenario-budget-2026-10-23--review"></a>
### Review

Round 1 used two reviewers, one on the backend and CLI and one on the dashboard and docs.

- **Backend and CLI:** no findings. The reviewer checked every scenario path that handles the acceptance choice for a budget counterpart (storage on both dialects, triggers on upgraded databases, projections, fingerprint, library, remote, inheritance, outage handling) and apexctl's refusal check against the controller's wording.
- **Dashboard and docs:** two low test gaps, no code defects.
  - The revise test's name claimed "another policy" without sending one. It now chooses another policy and checks its ID is sent.
  - The revise refusal test's mock answered every read alike, so it did not prove the create judged a fresh read; and the builder test's name claimed a deleted policy it did not cover. The revise test now offers a policy that is usable when the select loads and revised to another window by the time of the create, and the builder test's name says what it covers.

<!-- Original record: docs/qa/reviews/roadmap-telemetry-scenario-policy-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-telemetry-scenario-policy-2026-10-22"></a>

## Scenario telemetry acceptance policy — 2026-10-22


This [TEL-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-b--policy-evolution-and-complete-consumers) closes three items that the [policy selection slice](#roadmap-telemetry-policy-selection-2026-10-22) left open: dashboard selection, plain `apexctl run`, and scheduled and execution-plan runs.

Before this slice, a run could choose its telemetry acceptance policy only on `POST /api/v1/tests`, `POST /api/v1/ci/runs` and remote runs. A schedule or an execution-plan entry had no request of its own to name one, so it was always judged by its project's default. The dashboard and `apexctl run` offered no way to choose.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--contract"></a>
### Contract

<a id="roadmap-telemetry-scenario-policy-2026-10-22--scenario-choice"></a>
#### Scenario choice

- `telemetry_acceptance_policy_id` is a scenario field on:
  - `CreateScenarioRequest` (`POST /api/v1/scenarios`, `PUT /api/v1/scenarios/{id}`);
  - `StoredScenario`;
  - the authoring projection (`ScenarioAuthoringDocument`).
- It is stored on the scenarios table (`telemetry_acceptance_policy_id`, schema v75, renumbered v81 when merged with `origin/main`, SQLite and PostgreSQL), NULL when the scenario uses its project's default.
- The column is authored content. It is in `scenarioAuthoredColumns` (the revision trigger) and in `scenarioRuntimeCommitFingerprint`, so a change to it alone mints a new content revision.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--at-save"></a>
#### At save

`storeScenarioLockedWithSources` calls `validateRunTelemetryAcceptancePolicy` with the scenario's project, derived from its scripts, outside the runner lock, when the save changes the choice, or keeps it while the scenario's project (derived from its scripts) differs from the one the runner has for it. A save that keeps the scenario's current choice is not judged again: an unrelated edit, a library import update or a journal rollback never fails on a choice deleted since, or on a policy read. Runs that inherit it check it again. The rules match those at run creation:

| Choice | Answer |
| --- | --- |
| A policy the project lacks, or another project's | `400` |
| An invalid policy (`errRunPolicyConfiguration`) | `409` |
| A failed read (`errRunPolicyStoreUnavailable`) | `503` |

Empty and `project_default` both store no choice. `runPolicyErrorStatus` maps the statuses for create and update. Both routes already document `409` and `503`, so `go generate ./pkg/api` changed only the field.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--inheritance"></a>
#### Inheritance

`createTestRequestFromScenarioRevision` copies the scenario's choice into a request that names none. Every entry point that runs a scenario goes through it: a direct run with `scenario_id`, a schedule occurrence and an execution-plan entry.

- A request naming another policy, or `project_default`, keeps its own choice.
- A revised copy still inherits from its source run, not from the scenario.
- An inherited choice is then validated like any run's choice. One deleted since the scenario was saved refuses the run with a message naming the scenario's choice and the way out (`telemetryPolicyFromScenario`). It is never silently judged by the default.
- **Schedules.** A schedule occurrence refused this way is recorded as `failed`, including for a governed schedule (any created through the API), whose sweep first builds a capacity preview.
- **Policy-read outages.** When the policy read fails (`errRunPolicyStoreUnavailable`) for a choice inherited from the scenario, the run is still created with the choice. That covers a schedule occurrence, a plan entry, and a direct run naming no policy. The choice was validated when the scenario was saved, and Start pins it or refuses.
  - This slice changes only creation. Start reads the project's policies of every kind for every run (`resolveRunPolicyPinsForRun`), so a run started while they cannot be read is refused at Start, with or without a choice, as before this slice. A schedule or plan entry that starts its run at once during the outage records that Start failure.
  - A run naming its own policy is still answered `503` at creation.
- **Remote runs.** A remote run of a scenario with a choice must name the remote policy or `project_default`. Otherwise it is refused (`409`, `errRemoteRunScenarioPolicy`), because the pushed bundle omits the choice. The remote would judge the run by its linked copy's own choice or its project's default, never by the local choice.
- **Estimates and capacity previews.** These builds skip the check (`skipTelemetryPolicyCheck`), so a lost or unreadable choice never fails one or hides a schedule preview's capacity findings:
  - an editor's capacity estimate (`EstimateDraftCapacity`) and load-model check;
  - a schedule's capacity previews: the one shown when a schedule is created or updated, and the conflict preview a governed schedule's sweep builds before each occurrence;
  - a plan entry's secret preflight.

  The dashboard's estimate request omits the field. Launch preflight and every real create still judge the choice.
- **At Start**, the run's choice is pinned exactly as in the earlier slice.
- **Locking.** Inherited choices are validated in `buildManagedTestLocked`, as run-level choices already were. That runs on a build snapshot of the runner, with the live runner lock released. A schedule sweep, plan entry or direct run of a scenario with a choice now reads the policy store there.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--library-bundles"></a>
#### Library bundles

A policy ID names one of the source project's policies, and a bundle does not carry policies. So the choice is not portable:
- `normalizePortableScenarioRequestDefaults` clears it. An export omits it, and the library content hash and the recovery hash ignore it, so an existing bundle's hash is unchanged.
- `importLibraryScenario` replaces any choice a bundle names. A new import uses the destination project's default. An import that updates a linked scenario keeps that scenario's own choice.
- The rollback journal restores a scenario from `createScenarioRequestFromStored`, which keeps the choice.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--dashboard"></a>
#### Dashboard

- `TelemetryPolicySelect` lists the project's live telemetry acceptance policies with `analysisPoliciesApi.list(projectId, 'telemetry_acceptance_policy')`, which needs viewer access. It names the project's default (`apply_by_default`). A choice the list no longer offers stays visible as "no longer available", so a save never drops it silently. A failed load keeps the current choice and says so.
- **Accessibility.** The select is described by its explanation and, when there is one, by its inline error (`aria-describedby`, `aria-invalid`). The inline error is not announced separately: a blocked save or create announces it in the page error. Without a project nothing is read and no failure is claimed.
- **A stored choice the project lost** is shown as a warning (`settings-inline-warning`, described by the select, not `aria-invalid`) that runs created from the scenario are refused until another policy or the default is chosen. A save that keeps it succeeds, as the controller allows.
- **Scenario builder** (Run settings → Advanced settings).
  - The select edits `ScenarioFormState.telemetry_acceptance_policy_id`, carried by the draft (`ScenarioDraft.telemetryAcceptancePolicyId`) into the save and run payloads.
  - A draft checkpoint whose choice is not a string is not restored.
  - **Save check.** Under the save lock, a choice that differs from the loaded scenario's stored choice is judged against a fresh read of the project's policies (`telemetryPolicySaveIssue`).
    - One the project no longer has blocks the save before the save request, opens the Advanced settings panel and reloads its list, which marks the choice.
    - A choice the scenario already stores is not judged, as the controller does not judge it.
    - A read that fails or takes more than 10 seconds blocks nothing, because the controller decides. Closing the builder or leaving the scenario hub ends the read, and the save it guarded is dropped rather than sent unchecked.
  - Clearing the choice saves none, which returns the scenario to the default.
- **CreateTestModal**, which the dashboard opens only to revise a queued run.
  - The source run's choice is shown as "Keep the source run's choice (name)", marked "no longer available" when the project lacks it. Keeping it omits the field, so the server's inheritance applies.
  - "Project default" sends `project_default`, and another policy sends its ID.
  - **Create check.** A kept or chosen policy the project no longer has blocks the create, judged against a fresh read. The modal clears the previous error and shows it is busy from the start of that check. Its fields stay editable, so keyboard focus is never lost. If the settings change while the policy is checked, the copy is not created from the submitted snapshot: the modal asks for a review and another create. A block reloads the select's list. The modal cannot be closed while it is busy (the read lasts at most 10 seconds). If it unmounts meanwhile, the read ends and the copy is never created.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--apexctl-run"></a>
#### `apexctl run`

`apexctl run --api URL --telemetry-policy ID`:
- sends `telemetry_acceptance_policy_id` on `POST /api/v1/tests` (`runcontract.CreateTestRequest`);
- requires the create response to echo it (`runcontract.TestStatus`). Otherwise the queued run is deleted before Start (`deleteQueuedTestContext`), and the command fails with `cliConfigurationError` (exit `2`), with a message for a missing echo and another for a different policy. Any delete answer but success is checked by a read. A `404` can be a run the controller will not delete now, and a `500` can follow a delete that removed the run but not all its files. If the run can still be read, it is still queued: the command names it and exits `3`. A command deadline that passes meanwhile keeps that outcome beside the timeout, so it is never hidden;
- treats a controller refusal of the choice itself (`400` or `409` naming the telemetry acceptance policy: missing, over-long or invalid, matched in the controller's own wording) as a configuration error (exit `2`), as `apexctl ci run` does. Other refusals keep their usual exit `3`;
- needs `--api`. `project_default` is the same as omitting the flag.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--validation"></a>
### Validation

- **`TestScenarioTelemetryPolicyIsAuthoredContent`** (storage; also the `telemetry_acceptance_policy` case of `TestPostgresScenarioContentRevisionIntegration`):
  - the choice stored and read by `GetScenario`, `ListScenarios` and the authoring snapshot;
  - a change to it alone, including a direct column write, mints a new content revision;
  - clearing it.
- **`TestSchemaReleaseFingerprintIsCurrent`** records schema v75 (v81 after the `origin/main` merge, which also renumbered the run-level slice's release to v80, after main's v73–v79).
- **`TestScenarioTelemetryAcceptancePolicyCarriesThroughEveryEntryPoint`**:
  - `400` for a missing policy and for another project's policy, with the same message;
  - `409` for an invalid policy;
  - `project_default` stores none, and a choice with spaces is trimmed;
  - the choice in the API response, the store, the authoring projection and a runner reloaded from the store;
  - inherited by a direct run, an execution-plan entry and a schedule occurrence, while a run naming another policy or `project_default` keeps its own.
- **`TestScenarioTelemetryAcceptancePolicyGoneRefusesInheritingRuns`**:
  - a deleted choice refuses the inheriting run, naming the scenario's choice;
  - a run naming `project_default` is accepted;
  - an unrelated edit keeps the deleted choice;
  - a save choosing a missing policy is refused, and a save clearing the choice is accepted.
- **`TestScenarioTelemetryAcceptancePolicySaveStatuses`**:
  - `409` for an invalid policy on update;
  - `503` on create and update while the policies cannot be read;
  - an edit keeping the choice, and a restore through `UpdateScenarioIfUnchanged`, succeed during that outage.
- **`TestScenarioTelemetryAcceptancePolicyKeptChoiceIsJudgedAfterAProjectChange`**: a kept choice whose policy was deleted is not judged in the same project, and is refused once the scenario's project differs from the runner's.
- **`TestScenarioTelemetryAcceptancePolicyScheduleOutcomes`**:
  - during a policy-read outage, a schedule occurrence, a plan entry and a direct run are created with the scenario's choice, and the occurrence's immediate Start fails (`start_failed`) as any run's would. A run naming its own policy gets `503`;
  - once the policy is deleted, the occurrence is recorded `failed`, naming the scenario's choice.

  The schedules are created through `CreateScenarioSchedule`, so they are governed and their sweep builds the conflict preview first.
- **`TestScenarioTelemetryAcceptancePolicyCreatedDuringOutageIsPinnedAtStart`**: a run created during an outage, started once policies can be read, pins the scenario's choice (`run_selection`), not the default.
- **`TestTelemetryAcceptancePolicyIsNotJudgedByCapacityEstimates`**:
  - an estimate (direct and through `POST /api/v1/tests/estimate`) naming a missing policy succeeds, and so does one during an outage;
  - a schedule's capacity preview of a scenario whose choice was lost still builds;
  - a launch-preview build still refuses the choice.
- **`TestRemoteScenarioRunNeedsAnExplicitTelemetryPolicy`**: a remote run of a scenario with a choice is refused (`409` through `POST /api/v1/remotes/{id}/runs`) unless it names a remote policy or `project_default`.
- **`TestScenarioTelemetryAcceptancePolicyIsNotPortable`**:
  - an export omits the choice, and the content hash ignores it;
  - a crafted bundle naming a policy imports with the default;
  - a linked import that updates the scenario keeps its choice.
- **`TelemetryPolicySelect.test.tsx`**:
  - the listed options and the default's name;
  - an unavailable choice, with its described inline error and `aria-invalid`;
  - the warning for a stored choice the project lost, styled and described as a warning, not an error;
  - a reload when asked;
  - an unavailable kept source choice;
  - a failed load, and no project at all;
  - the revise options;
  - `telemetryPolicyIssue` and `telemetryPolicySaveIssue`, including a failed read, a read that times out or is ended, and the stored choice not being judged.
- **`TestHub.test.tsx`**:
  - the scenario builder saves a chosen policy;
  - a stored scenario's choice survives a save without changes;
  - an unrelated edit (a rename) keeping a stored choice deleted since is saved with both, and the builder's capacity estimates never send the choice;
  - a save whose check is pending when the hub unmounts is dropped;
  - choosing a policy deleted after the select loaded is blocked by the fresh read at save, from Review, opening the panel whose list marks it. Clearing the choice then saves none;
  - the same block with the panel already open reloads the mounted select's list, which marks the choice.
- **`CreateTestModal.test.tsx`**:
  - a revised copy keeping the source's choice omits the field;
  - choosing the project default sends `project_default`, and choosing another policy sends it;
  - a kept source policy the project no longer has blocks the create until the default is chosen;
  - a policy deleted after the modal loaded is blocked by the fresh read, with the modal busy (its fields still editable) while the read is pending, and the reloaded select marking the choice;
  - settings changed while the check is pending are not created from the stale snapshot, and the next create sends the new choice;
  - a modal unmounted while its check is pending creates nothing.
- **`scenarioAssembly.test.ts`** and **`scenarioAuthoringDraft.test.ts`**: the choice round-trips from a stored scenario into the save and run payloads (not the estimate request) and through a draft checkpoint. Clearing it omits it, and a malformed one is not restored.
- **`run_telemetry_policy_test.go`** (`cmd/apexctl`):
  - the choice sent and confirmed;
  - an unconfirmed choice, either missing or another policy, deletes the queued run without starting it and exits `2`, and another policy is named;
  - an undeleted unconfirmed run exits `3`, naming it, even when the command's deadline passes during the delete. So does a `404` or `500` delete of a run that can still be read, while either answer for a run that can no longer be read counts as deleted;
  - controller refusals of the choice exit `2`: missing, over-long and invalid, in the controller's wording. An unrelated refusal, with or without a choice, stays `3`;
  - no choice sends none;
  - flag parsing.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--review"></a>
### Review

Round 1 used two reviewers, one on the backend and CLI and one on the dashboard and docs.

Backend and CLI:
- **Remote scenario runs** dropped the scenario's choice silently. They now need an explicit remote choice.
- **Transient policy reads** could fail a scheduled occurrence for good, and would fail imports and rollbacks. A save that keeps the choice is no longer judged, and the sweep retries.
- **`apexctl` exit codes.** A controller refusal exited `3`, and a failed cleanup delete still exited `2`.
- **Smaller items:** the message for a different echoed policy, the run-only wording at scenario save, and status tests.

Dashboard:
- **Revise modal.** A deleted kept source policy looked valid until the server refused it, and the modal did not block a gone choice.
- **Save check.** The builder's save check could use a list read for another scenario. It now reads fresh under the save lock.
- **Smaller items:** accessibility attributes, the no-project notice, a misplaced comment, and test gaps.

Round 2 used two fresh reviewers with the same split.

Backend and CLI:
- **Outage retries.** A policy-read outage failed plan entries for good. The round-1 schedule retry could launch an occurrence late, past its missed-run policy. An inherited scenario choice now survives the outage instead.
- **`apexctl`:**
  - a `404` delete was taken as proof of deletion;
  - any `400`/`409` exited `2` once the flag was set.
- **Smaller items:** a stored choice was not re-judged after its project changed underneath, and the schedule test did not cover the retry.

Dashboard:
- **Save check.** The builder judged a kept stored choice that the controller would accept.
- **Smaller items:**
  - the save-time read had no timeout;
  - the modal showed no busy state during its check;
  - the select did not reflect a blocked choice;
  - the tests did not prove freshness or that the panel opened.

Round 3 used two fresh reviewers with the same split.

Backend and CLI:
- **Outage at Start.** The docs claimed an outage never fails a schedule or plan entry. Start reads the policy store for every run, so the claim is narrowed to creation, which is all this slice changes, and the test comment is corrected. A test now shows a run created during an outage pinning the choice at Start.
- **`apexctl` classification.** It matched only one wording, so invalid-policy `409`s and over-long IDs exited `3`.
- **Delete check.** A `500` after a delete that removed the run was not checked by a read.
- **Locking.** The locking wording was inexact.

Dashboard:
- **Revise modal:**
  - it did not reload its select after a block;
  - it kept a stale error during its check;
  - its tests did not prove freshness, the busy state or an explicit choice.
- **Inline error.** It was announced twice, and alarmed on a stored choice that a save keeps.
- **Save-time read.** It outlived its screen.
- **Docs.** Two wordings were inexact.

Round 4 used two fresh reviewers with the same split. They found low-severity items only.

Backend and CLI:
- **Untested rules.** No test covered:
  - the kept-choice skip during an outage (the test's scenario stored no choice);
  - the project-change re-check.

  Both tests now fail when their clause is removed.
- **CLI delete message.** It called any failed read "still readable".

Dashboard:
- **Ended save check.** An ended check fell through to an unchecked save, and closing the builder did not end the read.
- **Untested reload.** No test proved the open select reloads after a block. The new test fails without it.

Round 5 used two fresh reviewers with the same split. They found low-severity items only.

Backend and CLI:
- **Hidden outcome.** A command deadline hid an undeleted run, or a refused input, behind the timeout.
- **Untested status.** The remote route's `409` had no test through the route.

Dashboard:
- **Warning shown as an error.** The warning for a lost stored choice was styled and described as an error.
- **Test did not edit.** The "unrelated edit" test made no edit.
- **Untested drop.** No test showed that unmounting drops a pending check's save. The new test fails without the abort clause.

Round 6 used two fresh reviewers with the same split. They found low-severity items only.

Backend:
- **Unasserted claims.** Two claims had no assertion behind them: an outage's Start failure, and a plan entry created during an outage.

Dashboard:
- **Overstated claim.** The modal's "closing ends the read" claim overstated what the modal does. It cannot be closed while busy, so the claim now says an unmount ends the read. A test shows an unmount creates nothing, and it fails without the guard.

Round 7 used two fresh reviewers with the same split.
- **Backend and CLI:** no findings.
- **Dashboard and docs** (medium): the builder's capacity estimate sent the scenario's choice. The controller judged it there, so a lost or unreadable choice failed every estimate while editing. Estimates no longer judge or send the choice, and both the server test and the dashboard test fail without their fix.

Round 8 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend** (low): a schedule's capacity preview, and a plan entry's secret preflight, failed on a lost choice and dropped the error. That hid the schedule's real capacity conflicts. These capacity builds now skip the check like estimates. The schedule preview's test fails without the fix. The plan entry's secret preflight skip has no test of its own: without it, a lost choice only delays that entry's refusal from the preflight to its create.

Round 9 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend:**
  - **Medium.** A governed schedule's sweep built its conflict preview with the policy check. With a lost choice the sweep failed before any occurrence existed, so it recorded no `failed` occurrence and retried silently. The preview now skips the check. The schedule test now uses governed schedules created through `CreateScenarioSchedule`, and it fails without the fix.
  - **Low.** The round-8 note overstated test coverage of the plan secret preflight. It now says that skip is untested.

Round 10 used two fresh reviewers with the same split.
- **Dashboard and docs:** no findings.
- **Backend (low, wording):** the docs said a remote run naming no policy would be judged by the remote's default. The remote's linked copy can carry its own choice, so the wording now says the remote judges the run by its own copy's choice or its project's default, never the local one.

Round 11 used two fresh reviewers with the same split.
- **Backend and CLI:** no findings. Two wording nits were also aligned: a test comment and the `apexctl` help for an undeleted run.
- **Dashboard** (low): the revise modal stayed editable during its policy check, while the create sends what was submitted. (Round 12 replaced the round-11 fix below.)

Round 12 used two fresh reviewers with the same split.
- **Dashboard** (low): the round-11 fix made the fields read-only with a disabled fieldset, which dropped keyboard focus when a field submitted the form. It is replaced. The fields stay editable, and a form changed during the check is not created, using the fingerprint the modal's dirty tracking already keeps. The test fails without the check.
- **Backend and CLI:** no findings. One optional wording point was taken: the guide now says any delete that cannot be confirmed exits `3`.

Round 13 used two fresh reviewers with the same split. Neither found anything.

Gates on the reviewed code (before the `origin/main` merge):
- the full `pkg/api` suite;
- the PostgreSQL storage suite (`-tags postgres_integration`), including `TestPostgresScenarioContentRevisionIntegration/telemetry_acceptance_policy`;
- the dashboard's full Vitest suite, `typecheck`, `lint` and `contracts:check`;
- `cmd/apexctl`, `pkg/runcontract` and the scenario storage tests.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--merge-with-originmain"></a>
#### Merge with `origin/main`

`origin/main` added run analysis policy selections (`RunAnalysisPolicySelections`, `analysis_policy_selections_json`) and schema releases v73–v79. The merge resolution:
- **Schema releases.** Main keeps v73–v79. The run-level slice's release becomes v80 (`run-telemetry-acceptance-policy`) and this slice's becomes v81 (`scenario-telemetry-acceptance-policy`), with fingerprints recomputed on the merged schema.
- **`tests` table.** `sqlite_schema.go` and `sqlite_tests.go` keep both sides' columns. The insert has one placeholder per column.
- **Policy pins.** `resolveRunPolicyPinsForRun` pins a telemetry acceptance choice by ID and refuses one that no longer exists. Main's browser and journey selections go through `selectedRunAnalysisPolicy`, and every other kind uses the project default. The start path (`persistManagedTestStartOutsideLockLocked`) passes both the selections and the acceptance choice.
- **Dashboard.** `scenarioDraftUtils.ts` keeps both fields. `ArchivedNativePanel.tsx` and `NativeAnalyticsPanel.tsx` take main's version, plus NA-C's byte metrics.
- **Combined pins.** `TestRunPolicySelectionsAndChosenTelemetryPolicyPinTogether` covers the case where a run carries both a browser selection and a telemetry acceptance choice, and both are pinned. With only a browser selection, the acceptance policy falls back to the project default.

Gates on the merged tree:
- the full `pkg/api` suite;
- the PostgreSQL storage suite, including `TestPostgresScenarioContentRevisionIntegration/telemetry_acceptance_policy`;
- the dashboard's full Vitest suite, `typecheck`, `lint` and `contracts:check`;
- `cmd/apexctl`, `pkg/runcontract` and `pkg/storage`.

A reviewer of the merge found only stale documentation statements, which are fixed.

<a id="roadmap-telemetry-scenario-policy-2026-10-22--remaining"></a>
### Remaining

- **Telemetry budget policy.** A run or scenario cannot choose a telemetry budget policy (`telemetry_policy`).
- **Library bundles** do not carry a scenario's choice. A team that moves scenarios between projects chooses again in the destination.
- **Rest of TEL-B.** In-run policy segments and the rest of the TEL-B consumer matrix.
