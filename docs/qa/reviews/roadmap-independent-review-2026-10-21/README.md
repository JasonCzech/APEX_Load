# Independent review of documentation and roadmap commits — 2026-10-21

## Scope and method

The requested scope includes every commit from this task for which retained records
could not establish an independent-agent review. Earlier records saying “review”
or “cross-commit review” were not treated as proof of independent review.
Three separate agents reviewed the original source commits and their composed
current implementation, including the repackaged browser stack and later durable
replay work. Findings were checked against current code to avoid repairing defects
already resolved by later slices.

- `review_baseline_admission`: baseline admission, replay storage/publication/recovery,
  lifecycle shutdown, telemetry cursors/windows, evacuation and notification outbox.
- `review_original_qualification`: native browser sources, clocks, authored identity,
  document qualification, protocol fixtures, native HAR limits/finalization and ownership.
- `review_cross_commit`: browser intelligence/policies/panels/dashboard contracts,
  analytics/import/authoring/Java changes, and documentation/specification preservation.

The documentation audit included cleanup commits `0e9a693ff`, `63c437863`,
`a458a82eb`, `741491009`, `e6bb08fe9` and both specification commits below.
The review checked contracts, evidence destinations, indexes and in-app help
registration/navigation. Generated bundles and every historical fixture were not
individually audited. This is a scoped cross-commit review, not a claim that all
external qualification lanes have been executed.

## Findings and corrections

1. Browser baseline comparisons could score blank/foreign document groups without
   proving membership and authored identity in both workloads. Qualification now
   includes every observed browser group, with an archive-to-detector regression.
2. Original-source identity was read from final provenance while workload identity
   used initial provenance. Both now use initial provenance when present; missing
   initial source data cannot fall back to a final descriptor.
3. Baseline reference-key aliases bypassed the canonical finalization/self checks
   and could freeze noncanonical requests. Admission resolves canonical IDs before
   checking or freezing the baseline.
4. Automatic analysis was lost when a pinned baseline was still finalizing.
   Durable admission intents retry before freezing immutable analysis inputs.
   The existing archive-commit-to-attachment-hook crash gap remains outside this fix.
5. Java documentation retained stale delivery claims for implemented experimental
   dedicated components. Contracts, broker guide and release-gate summary now
   distinguish implemented opt-in components from missing external qualification.

Follow-up independent review identified three defects in the initial retry fix:
publication needed an atomic run-identity fence; transient settings-read errors
must not acknowledge an intent as disabled; legacy default-project ownership
must be normalized. A weak empty-initial-provenance test was also strengthened
so that an incorrect fallback would fail the regression. Validation also exposed
replacement fixtures that used `SaveTest`, which intentionally preserves initial
provenance; those tests now prove an actual replacement before asserting fences.

The full-suite run exposed two distributed fixture defects: the manually created
evacuation run omitted production's per-run assignment renewal loop, and the
slow-persistence ordering test allowed only 400 ms for teardown RPCs. The fixture
now owns and joins the renewal loop; that ordering case uses a two-second RPC
budget while preserving its barrier assertion. The separate lead-consumption
regression retains its tight budget. Independent re-review of these repairs was clean.

## Validation and final review

Final independent code-review rounds were clean, including re-review by agents
other than the retry implementation's author. All severities were addressed before
commit; the corrected fixture tests also received a clean independent review.

- Targeted API, storage and RCA checks passed, including embedded API documentation
  handlers. The initial replacement-test fixture failure was corrected and rerun.
- Race-enabled API, storage and RCA regressions passed: API 158.583 seconds,
  storage 54.601 seconds, RCA 3.247 seconds. This includes both distributed fixture
  repairs and the admission/qualification tests.
- The first full Go suite passed 166 packages and skipped 10, but failed two API
  fixtures (repaired above) and a Java renewal test during a wall-clock interruption.
  The unchanged Java test passed five consecutive isolated reruns in 19.089 seconds.
- The private attached HAR imported successfully (HTTP 200): 803 requests,
  476 request actions, 327 filtered assets, 479 assertions, 37 extractors,
  43 correlations, four warnings, 264 protected requirements and zero DSL validation
  issues. The input ZIP SHA-256 remains
  `7469ebdef88a576cd3cfa5891318a4aa5afb4efe97fccb410df339fc609ddef7`.
  The private archive was imported, not replayed, and is not committed.
- Final `go test -overlay=<private HAR overlay> -json -timeout=20m ./...`
  passed with `GOFLAGS=-p=1`: 168 packages passed, 10 skipped, zero failures.
  The API package passed in 889.010 seconds; the attached HAR check passed again
  in 43.270 seconds with the same structural counts and input digest.
- Latest `origin/main` was fetched and already integrated before both full runs.
  Documentation checks passed for 330 active Markdown files, and whitespace checks
  passed. The full suite covered generated documentation and embedded API-doc handlers.
  No UI behavior changed in these fixes; previous browser evidence is preserved,
  not presented as a new browser workflow run.

Raw local results are in `/private/tmp/apex-browser-durable-backend/`:
`independent-targeted-final.log`, `independent-race.log`,
`independent-full.jsonl`, `independent-full-final.jsonl`,
`independent-java-repeat.log` and `independent-har-summary-final.json`.

External reference hosts, native Linux/Windows runners,
sanitized TruClient exports and disposable multi-host fault qualification remain
unavailable; no local test is represented as those gates.

## Original source commit inventory

```text
98d55b89a docs: specify remaining roadmap implementation
4f75f1ac4 docs: refine roadmap acceptance and evidence requirements
57c7ff947 fix: prevent telemetry coverage inflation from publication replays
8f120a25f feat: retain source collection window provenance
07c25d409 feat: persist telemetry cursors and recover source windows
0c51cc296 fix: use monotonic local browser telemetry receipts
dd1e8308f fix: decode Java HTTP request bodies exactly once
dd397bbd7 fix: require source offsets for absolute analytics timestamps
b5a263a33 fix: translate coordinated barriers through the published clock
0dc6152eb fix: reject ambiguous native analytics local timestamps
86a9f5cdb build: refresh embedded dashboard after roadmap telemetry changes
0cea94070 fix: refuse unqualified browser baseline comparisons
5486af9c6 fix: bound native HAR finalization while retaining ownership
90c2c84a0 fix: preserve archive approval handoffs and sibling drafts
2c4cdc26f fix: retain scenario prerequisite import drafts
23dbe470b fix: reject unsupported empty Java HTTP encodings
e86de66a6 fix: bound same-leader evacuation cleanup with durable intervention
d0d54cd96 feat: persist bounded telemetry notification delivery outbox
82376d1bf fix: bound native HAR outstanding requests and load-state waits
a604a431e feat: deliver durable telemetry acceptance notifications
aaa3bac0e fix: make browser benchmark memory units explicit
cb89e0b9e fix: bound retained joint evidence before copying and queuing
c74e87709 test: qualify native import partition precedence
0f3da9cf1 fix: negotiate mapping-preserving dataset associations
72188c255 feat: retain qualified Java HTTP representation evidence
f831f9465 test: qualify controlled gRPC and WebSocket measurements
5b1d23d5f feat(analysis): bind decimal rounding consent to source revisions
241b3fe7f docs: reconcile remaining roadmap work and qualification gates
51b17eb4a fix(browser): separate interaction maps by recorded cohorts
92bcb180a test(browser): qualify evidence across browser process loss
e2eeb02dc feat(browser): bind load and replay to authored step identities
79e6f4ad2 feat(browser): retain negotiated native interaction dispatch evidence
a04e2d561 test(browser): qualify native WebKit loss and Chromium BFCache lifecycle
782ff515a test(browser): qualify authenticated managed native evidence workflows
4e2e32099 fix(browser): retain native HAR capture omission evidence
ea0df819a feat(browser): enforce atomic evidence-backed intelligence promotion
fca7d90a5 fix(browser): tolerate completed diagnostic detach after HAR close
862d044de feat(browser): configure immutable per-metric baseline floors
3eb983b2b feat(browser): retain original authored source across replay and runs
eeab25b6e test(command-center): verify grouped transaction row ownership
6972519be test(help): await anchor realignment after viewport resize
173756882 feat(browser): select immutable browser and journey policies per run
64dc75990 docs(qa): refresh tracked browser workflow count
ce469a793 feat(browser): retain negotiated post-action outcome clocks
df30ffa15 feat(browser): retain separate browser cohorts in saved panels
8d5c74d9c fix(browser): snapshot native request timing under synchronization
1948977fd feat(browser): retain authored navigation page binding receipts
57adb6119 test(browser): qualify rendered native waterfalls and cache evidence
0cc84fa6f test(analytics): keep imported RCA telemetry fixtures floating point
56ef24845 feat(browser): freeze evaluated archive inputs in saved panels
35ce7c99b fix(build): align continuation with reviewed Go 1.26.10 toolchain
6b99d8ff5 feat(storage): retain fenced browser replay artifact receipts
8164b8e93 fix(api): join execution finalizers during shutdown
a65a43804 test(api): close recorder import fixture sessions
6651a741d docs: reconcile remaining browser implementation work
6f826fc7e feat(api): coordinate durable replay IDs without HA
42c91c743 fix(browser): bound keyboard heatmap aggregation
```

Additional reviewed integration commits: `10a7c7bb4` (workflow-store isolation),
`b71da0971` (imported analytics fixtures), `4742df101` (toolchain pin), and
`3ed285386` (CI cleanup trap documentation).

## Composed browser stack inventory

These commits include repackaged earlier changes; the overlapping source was
reviewed once as a composed implementation rather than counted as new behavior.

```text
6e38d2eab feat(browser): retain and qualify native evidence foundations
9da727d87 feat(browser): pin analysis policies and gate intelligence promotion
e8e4f8717 feat(browser): retain native outcome clocks and authored page receipts
37dddb482 feat(browser): freeze evaluated archive inputs in saved panels
8227f4aac feat(storage): retain fenced browser replay artifact receipts
744bee037 fix(api): join execution finalizers during shutdown
0e75db129 test(api): close recorder import fixture sessions
41ba279ea docs: reconcile remaining browser implementation work
7014cfa89 feat(api): coordinate durable replay IDs without HA
b1be83920 fix(browser): bound keyboard heatmap aggregation
332b04e8a feat(browser): add immutable replay artifact backend
ad4d14def feat(browser): fence terminal replay artifact publication
de3a69699 feat(browser): add lease-bound replay artifact publisher
c972ff566 feat(browser): add replay recovery scope and cleanup paging
7cc82b4f1 fix(browser): bind replay cleanup to persistent volume identity
9a3d2e931 Preserve verified terminal replay evidence during recovery
58dd6bcc5 Bound replay recovery reads and exact artifact cleanup
dce940c0e feat(browser): recover durable replay evidence through controller routes
53c2b897f feat(browser): retain replay capture omission evidence
dbb60bf2f fix(browser): qualify baseline cadence and frozen engine identity
94c9f5a90 feat(browser): freeze policy-pinned RCA baselines at admission
8c689c99f fix(browser): qualify baselines against original authored sources
```
