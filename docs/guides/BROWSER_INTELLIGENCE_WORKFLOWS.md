# Browser intelligence workflows

Status: implemented. This guide covers where a run's browser intelligence is read, how its policies are edited, and what reports, the CLI and MCP tools carry. The results themselves are described in [browser findings](#browser-findings-and-qualified-scores), [interaction evidence](BROWSER_INTERACTION_EVIDENCE.md) and [journey accounting](#journey-accounting).

## Guide map

Use this guide to interpret archived evidence and operate policies, evaluations, reports and exports. Capture and retention have separate contracts:

| Task | Reference |
| --- | --- |
| Capture page timings and document populations | [Browser document evidence](BROWSER_DOCUMENT_EVIDENCE.md) |
| Capture gestures, provenance and layout revisions | [Browser interaction evidence](BROWSER_INTERACTION_EVIDENCE.md) |
| Inspect retained requests and exact filters | [Retained resource inspection](RETAINED_RESOURCE_INSPECTION.md) |
| Understand native/synthetic HAR ownership and export bounds | [HAR working-file ownership](BROWSER_HAR_WORKING_OWNERSHIP.md) |
| Interpret findings and scores | [Findings contract](#browser-findings-and-qualified-scores) |
| Interpret journey outcomes and weighted risk | [Journey accounting](#journey-accounting) |
| Interpret gestures and heatmap coverage | [Patterns and heatmaps](#patterns-and-heatmaps) |

## The Browser area of a run report

A run with browser groups, browser document evidence or a pinned browser policy (a project default is pinned at Start) has a **Browser** area in its run report (`#/report/{id}?section=browser`). It reads the finalized archive. On an HA controller, a panel says the archive is not available yet until it is published, and offers to check again. The area holds:

- **Device, engine and group comparison.** For each engine, device profile and scenario group, it shows each document metric's p75 over final qualified main-frame navigation documents, with how many documents it counts.
  - The p75 is computed as the controller computes it: the upper bound of the bucket holding it, capped by the observed maximum.
  - Documents record no page, so the scenario group stands in for one.
  - One engine, device and group that ran under more than one runtime cohort (for example, different network shaping) gets one row per cohort.
  - Child-frame and back-forward cache tuples are counted and left out.
- **Browser findings and the qualified score** under the run's pinned browser policy. For a run that pinned a browser policy, the area offers editors an on-demand evaluation under it; anyone who can view a run may also request an evaluation, under the pinned policy or a named revision, through the API or `apexctl`. An evaluation under a named revision is read from that evaluation and never replaces the run's findings shown here.
- **Browser baseline.** The baseline comparison of the run's latest root-cause analysis (anomalies, qualification and exclusions). Without an analysis, the area links to root-cause analysis.
- **Interaction patterns and heatmaps.**
- **Journey risk.**
- **The retained resource inspector.**

The live execution page shows the same comparison over the documents published so far, together with **Provisional live browser intelligence** (findings, interaction patterns, and journey risk) evaluated on a 30-second cadence from bounded incremental document, interaction, and journey evidence (`provisional: true`, baseline status `not_evaluated_live`, and `live_truncated: true` if in-memory live caps are exceeded). Provisional live snapshots are persisted through `LiveStateStore` so HA followers serve identical provisional responses while the run is active or finalizing, and are atomically replaced once the finalized archive is attached.

### Drill-down: finding → document → resources → HAR entry

1. A finding lists its worst documents. Selecting one opens the retained resource inspector filtered to that document's requests (the `document_id` filter).
2. Each request's details offer **Show this request's HAR entry** to users who may download HARs (editors and project administrators). It reads `GET /api/v1/tests/{id}/archive/browser/har-entry?request_id=…`:
   - **Synthetic entry.** The request is returned as a synthetic HAR 1.2 entry, scrubbed and bounded exactly as the downloadable synthetic HAR is, with its `_apexload.request_id`.
   - **Native HAR.** A request whose browser context recorded a native HAR is not in the synthetic HAR. The response names that native HAR instead, and the inspector offers to download it. Native HARs recorded before they named their browser context cannot be matched to a request; when the archive has any, the response counts them, since the request may be in one.
   - **Left out.** A request the synthetic HAR leaves out (for example a URL with no host) says why, as a downloaded synthetic HAR does.
   - **Unknown request.** A request identity the archive does not hold answers 404.
   - **Busy.** The read scans the archive and shares the resource inspector's one scan at a time and its time limit: while another scan runs, or when it runs out of time, it answers 503 and can be retried.

## Editing browser and journey policies

**Settings → Workspaces → Browser & journey policies** lists a project's browser and journey policies. Editors, project administrators and system administrators can create, edit and delete them; viewers can read them.

- **Browser policy fields:** the scored metrics with their good and poor bounds and weights, the engines and device profiles it applies to, the effect floors (which only raise the defaults), and the baseline comparison mode.
- **Journey policy fields:** journeys with a transaction or action start and success boundary, a weight, and whether the step-risk index is reported.
- **Revisions:** every save names the revision it was read at. A policy someone changed in the meantime is refused, and the editor offers to load the current revision.
- **Past revisions:** any past revision can be shown read-only.
- **Defaults and pins:** a policy marked as the project default is pinned when runs start. Runs keep the revision they pinned, and findings keep the revision they were evaluated under, so editing a policy never rewrites a finished run's results.

## Saved panels

Project analytics panels can plot **Archived browser documents**: a document metric (`document_lcp`, `document_fcp`, `document_inp`, `document_cls`) at p50 to p99 over elapsed time.
- **One tuple per panel (or separate cohorts):** a single-tuple panel reads one measurement tuple, named by engine, device profile, group, region, runtime cohort, frame scope and lifecycle; a separate-cohort panel (`browser_tuple_mode: "separate"`) plots up to 20 complete tuples independently.
- **Frozen archive, policy, and baseline selection pins (`query_version: 5`, `BrowserPanelInputs` `version: 2`):** saving a comparison to a panel from **Baseline comparison → Save to panel** evaluates and pins the current and baseline archive manifest SHA-256 digests, sorted tuple IDs, immutable `browser_policy` and `journey_policy` revision pins (`kind`, `id`, `revision`, `sha256`), and the matched root-cause analysis `selection_receipt_sha256` (`browser_selection` receipt digest). Reopening via `POST /api/v1/tests/{id}/archive/native-series` or `apexctl analysis replay-panel` verifies that pinned policy revisions have not been tombstoned and that the selection receipt and archive digests match, while keeping `query_version: 4` (`version: 1`) saved panels readable.

## Reports

The engineer report profile adds three sections, in HTML, PDF, CSV and (through HTML) DOCX:
- **Browser Findings:** the score and up to 20 findings, active ones first.
- **Interaction Patterns and Heatmaps:** coverage, up to 20 patterns and 12 heatmaps.
- **Journey Risk:** the weighted failure index and each journey's attempts.

Each section reads its result when the report is exported. A section whose result could not be read (no evaluation, an archive not finalized, no pinned journey policy) says why instead. The browser baseline comparison is part of the root-cause section, with the analysis it comes from, for projects that present root-cause v2 (`APEXLOAD_RCA_V2_PRESENT_PROJECTS`); the v1 root-cause section has no baseline. The Browser area shows the baseline for every project.

## CLI and MCP tools

| Task | `apexctl` | MCP tool |
| --- | --- | --- |
| Findings and score | `analysis browser-findings --run ID [--evaluate …]` | `run_archive_browser_findings` |
| Interaction patterns and heatmaps | `analysis interactions --run ID` | `run_archive_browser_interactions` |
| Journey accounting | `analysis journeys --run ID [--policy ID [--revision N]]` | `run_archive_journeys` |
| One request's HAR entry | `analysis har-entry --run ID --request-id ID` | `run_archive_browser_har_entry` |
| Policies | `analysis policies list\|get\|save\|delete --project ID …` | `analysis_policy_list`, `_get`, `_create`, `_update`, `_delete` |
| Replay saved panel | `analysis replay-panel --id ID [--revision N]` | — |
| Pin policies on run / CI / schedule | `run --api URL [--browser-policy ID@REV] [--journey-policy ID@REV]`, `ci run [--browser-policy ID@REV] [--journey-policy ID@REV]`, `schedule preview\|create\|update --scenario ID [--browser-policy ID@REV\|project_default] [--journey-policy ID@REV\|project_default]` | — |

`apexctl analysis policies save` with `--id` needs the `--revision` the policy was read at, as the editor does.

## Acceptance

Automated acceptance suites verify each of these behaviors, including a golden page run in Chromium and WebKit against measured ground truth.

## Browser findings and qualified scores

Browser findings judge a run's archived document metrics against a
versioned `browser_policy`, server side. They produce two things:

- versioned finding records (`browser_findings.v2`);
- a weighted score (`browser_score.v1`).

Only qualified final observations count. The dashboard's legacy summary
estimate (`legacy-summary-availability-v2`) stays for runs that have no
server result.

### Policy

A `browser_policy` (Analysis policies, `POST /api/v1/analysis/policies`)
contains:

- **Metrics:** up to four of `fcp`, `lcp`, `cls` and `inp`. Each has a
  `good` and a `poor` threshold (`0 <= good < poor`) and a non-negative
  `weight`. A metric with a positive weight is **required** for an overall
  score.
- **Applicability:** optional engine (`chromium`, `webkit`) and device
  profile lists. A cohort outside them is suppressed, never scored.
- **Effect floors and baseline mode:** these are used by RCA's browser
  baseline anomalies (`browser_baseline.v1`) and populated into each finding's
  `baseline` verdict under `browser_findings.v2`. The floors can only raise its
  minimums. Mode `none` turns the comparison off (`status: not_selected`,
  `reason: policy_baseline_off`), `pinned_run` compares only when the
  analysis's baseline is that run, and `latest_compatible` uses the analysis's
  selected baseline run. When a completed RCA analysis matches the run's
  archive digest and the evaluated policy's `PolicySHA256`, each application
  outcome finding records `regressed`, `within_baseline`,
  `insufficient_evidence`, `unqualified` (with its reason), or `not_selected`
  (never healthy when a baseline is missing), with deterministic worst-status
  precedence across multi-load-level runs and immutable links to the RCA
  analysis ID, baseline run ID, baseline archive digest, `browser_selection`
  receipt digest, and pinned browser/journey policies; `collection_health`
  findings record `not_applicable`.

A policy marked `apply_by_default` is pinned to every run started in the
project, by revision and digest.

### When evaluation runs

Evaluation is a `browser_intelligence` evaluation job.

- **Automatically:** when a run's archive is finalized (if the run pinned a
  browser policy and requested document evidence), and again when a completed
  root-cause analysis commits for that run so findings reflect the evaluated
  baseline status without double-firing webhook notifications for unchanged
  active finding keys. An evaluation of the same inputs is never queued twice.
- **On demand:**
  - `POST /api/v1/analysis/evaluations` with
    `{"kind":"browser_intelligence","request":{"run":"<id>"}}`. Add
    `"policy":{"id":…,"revision":…}` for another revision.
  - The Evaluate button on the run report's Browser findings panel.
  - `apexctl analysis browser-findings --run ID --evaluate --project P [--wait]`.

The job's input fingerprint covers the rule set, the score version, the
policy revision's digest and the archive manifest's digest.

A run's own result is always an evaluation under its pinned policy. An
evaluation under a named revision is an ordinary evaluation job: read it from
the evaluation API (`apexctl … --policy …` prints the job, and with `--wait`
its result). It never
replaces the run's result and never sends webhooks. Evaluations are indexed
by their run (schema v53 `feature_evaluation_jobs.subject_id`), so a run's
result is found however many evaluations the project has.

### What is judged

Only main-frame navigations are judged. Child frames and back/forward-cache
restores are listed in the result's notes, never scored.

Each cohort (engine and device profile) is judged per policy metric. The
observed value is the nearest-rank p75 of the qualified final
observations' values.

Provisional, censored, unqualified and unavailable observations are
reported in the finding's `quality`, never in its value.

| Rule | Category | Produces |
| --- | --- | --- |
| `threshold` | application outcome | `active` with severity `needs_improvement` (p75 above good) or `poor` (above poor); `suppressed` outside applicability; `unscored` when the metric was not observed or has no qualified final observation. Within good, there is no finding. |
| `collection.incomplete` | collection health | `active` when the run's document evidence lost observations. |
| `collection.absent` | collection health | `active` when document evidence was requested and none was retained. |

Every finding records:

- the rule set, rule ID and version, and the category;
- the metric, algorithm and unit, the cohort and the run window;
- applicability, the statistic and observed value, and the thresholds;
- quality counts and baseline criteria;
- evidence: up to five of the worst qualified documents, each with the
  archive query of its retained resources;
- its state, with a reason when it is not active.

A record's `id` covers the evaluation's input fingerprint, so an identical
rerun reproduces it. Its `key` (run, rule, metric, cohort, window)
identifies the finding across evaluations.

### Score

Each policy metric's component is computed as follows:

- The p75 of the qualified final observations of every applicable cohort.
- Scored 100 at or below good and 0 at or above poor, linear between.
- Its contribution is `weight × score / Σ required weights`.

The overall score is the sum of the contributions:

- **Bands:** `good` from 90, `needs_improvement` from 50, `poor` below.
- **No score** when a required metric is missing (`missing_required_metric`,
  listing the metrics), or when no document evidence was retained
  (`no_document_evidence`).

When there is no overall score, the observed components and their
coverage are shown instead. The remaining metrics are never renormalized.

### Reading results

- **API:** `GET /api/v1/tests/{id}/archive/browser-findings` returns the
  run's latest ready result under its pinned policy (`status: ready`),
  `pending`, `failed` (the newest evaluation failed or expired, claimed
  repeatedly without finishing, and none is ready, with `failure`), or
  `not_evaluated`. Queuing the automatic evaluation is best effort: if it
  cannot be queued when the archive is attached (the evaluation quota, a
  changed pin, an archive that cannot be read), the controller logs it and the
  run stays `not_evaluated` until someone evaluates it.
- **Dashboard:** the Browser findings panel in the run report's
  [Browser area](#the-browser-area-of-a-run-report) shows the score, its components and contributions,
  the findings by state, and the notes. It checks a pending evaluation again
  until it settles.
- **Webhooks:** subscribe to `browser_finding` and `browser_finding_resolved`.
  When an evaluation under the run's pinned policy is stored as ready:
  - one `browser_finding` event lists the findings active now but not in the
    run's previous ready pinned evaluation;
  - one `browser_finding_resolved` event lists those active before and not
    now;
  - an identical rerun sends nothing, and a completion that stored nothing
    (cancelled, overtaken) sends nothing;
  - each event lists at most 20 finding keys.

## Journey accounting

Journey accounting reads a finished run's archived samples and counts how its
declared journeys turned out. The result is weighted test-journey risk under
a versioned policy. It is never revenue or conversion impact, which would
need an authorized external model.

### Declare journeys

Journeys live in a project `journey_policy` (Analysis policies,
`POST /api/v1/analysis/policies`). Each journey has:

- an `id` (lowercase slug) and a `name`;
- an explicit `start` and `success` boundary, each naming one authored
  `transaction` or one `action`. A transaction boundary matches a
  transaction sample of that name; an action boundary matches an action
  sample;
- a `weight` from 0 to 1000.

`step_risk_enabled: true` adds the separate step-risk index. A policy marked
`apply_by_default` is pinned to every run started in the project, with its
revision and digest. Revisions are immutable, so a later edit never
rewrites an earlier run's accounting.

### How attempts are classified

Every archived sample carries its VUser, its 1-based iteration and a random
VUser lifetime. A VUser ID is reused by later VUsers (open-workload
arrivals, scale-up); a lifetime never is, so each lifetime is its own
context. Each iteration is read in time order. Samples are archived as they
are recorded, so a transaction's row follows its own steps. Ordering by time
puts it back around them, using each transaction's wall-clock span
(`elapsed_ms`) when its duration excludes think time inside it:

- A start boundary opens an attempt when it begins.
- Action samples that begin while an attempt is open are its steps.
- The success boundary decides the attempt when it ends.

| Outcome | Meaning |
| --- | --- |
| completed | The success boundary succeeded. Failed steps before it are still counted as step failures. |
| failed | The success boundary failed, or the iteration ended after one of the attempt's steps failed. One failed journey counts once, however many steps failed. |
| aborted | The iteration ended, or the journey started again, without the outcome and without a failed step. |
| censored | The outcome is unknown, because the run (or a generator's share of it) ended while the attempt was open, or a stop or the generator's own resource limit cut it. |
| skipped | An iteration of a group that ran the journey somewhere never started it. A possibly cut iteration does not count. |

Eligible attempts are the completed, failed and aborted ones. Censored and
skipped attempts never enter the index.

#### Stops and interrupted samples

The engine marks a failure `interrupted` when it was recorded after the VUser
was told to stop, or when the load generator itself refused it. That covers
four cases:

- The action in flight was cancelled by a hard stop (or by Run's own context
  when there is no hard-stop context).
- A stop or goal boundary abandoned the iteration and left a failed
  transaction behind.
- A transaction the VUser still had open when a goal boundary ended it.
- A resource-limit failure (the error class `resource_limit`, for example a
  browser context closed at its HAR or memory limit): the generator, not the
  system under test, ended the action.

Work a stop cut off is left out of the results altogether, as LoadRunner
does: a request the stop cancelled, a transaction a hard stop cut off, and a
transaction still open when the run's stop or end, a ramp-down or a
scale-down ended the VUser. The attempt it was part of has no settling row,
so it is censored.

Neither cancellation nor a generator limit can establish a failure of the
system under test. An interrupted failure therefore censors every attempt it
cut and is not counted as a step failure. An attempt that still reaches its
outcome keeps it, and a boundary that fails after one of its attempt's steps
was interrupted is censored rather than counted as failed.

The last iteration of each VUser lifetime is the one a stop may have cut,
for instance during think time between transactions, where no sample shows
it. The engine therefore marks the rows it records because an iteration
ended on its own (`ends_iteration`): the iteration transaction, and the
transactions a VUser left open when it ended on its own (its iterations
done, or a genuine failure that ends it). An attempt still open in a
lifetime's last iteration is settled (failed or aborted) only when that
iteration has such a row that is not interrupted; otherwise it is censored.
A transaction still open when the next iteration starts is carried into it.

So scripts that record an iteration transaction (automatic transactions per
iteration, or none and no explicit transactions) settle every last
iteration that ended on its own. Scripts without one (explicit
transactions, or automatic transactions per action) settle a last
iteration only when the VUser ended on its own with transactions still
open; a last iteration that ended on its own with none open, or a
fail-fast failure outside any open transaction, is censored. In an open
workload every arrival runs a single iteration, so for such scripts every
arrival's unsettled attempt is censored. Censoring excludes an attempt from
the index rather than guessing its outcome. Whether a VUser ended on its own
(its iterations or data done, a genuine failure with continue on error off)
or was ended (a stop, ramp-down, scale-down or goal limit) is recorded by
the engine where its loop ends. A fail-fast failure while a shared
execution goal is spent is taken for the goal limit's, since a parallel
branch or WebSocket callback refused a goal slot fails the same way.

Rows without a lifetime (written before lifetimes existed) fall back to the
VUser ID: their last iteration is always censored, and a replacement
generator that continues an evacuated VUser restarts its iterations, so
the earlier share's last iteration is censored too. A group's once-only
init/teardown VUser records lifecycle rows without a lifetime; they are not
counted as rows without identity.

### Indexes

- **Weighted failure index:** `sum(weight × failed) / sum(weight × eligible)`.
  It is unscored (`no_weighted_eligible_attempts`) when the denominator is
  zero.
- **Step-risk index** (only with `step_risk_enabled`):
  `sum(weight × step_failures) / sum(weight × steps)`, counting the steps of
  eligible attempts. It is unscored (`no_weighted_steps`) when there are no
  weighted steps.

Accounting is bounded:

- 200,000 tracked VUsers;
- 10,000 buffered samples per open iteration;
- 1,000,000 buffered samples in total.

The report is also incomplete when the archive scan stops at its budget
(2,000,000 rows or 512 MiB), when a distributed run's generator sources were
not all collected, or when rows from a source without VUser identity (an
older generator) sit beside rows with it. An incomplete report lists its
`incomplete_reasons`, and both indexes are withheld
(`accounting_incomplete`). A run that recorded identity but ran no iteration
is `unavailable` with `no_iterations` (as is an archive with no samples at
all).

### Read the result

- **API:** `GET /api/v1/tests/{id}/archive/journeys` (viewer access). It
  applies the run's pinned `journey_policy` revision. To use another, pass
  `?policy=ID`, which takes its latest revision, or add `&revision=N`.
  - A run that pinned none is `unavailable` with `no_journey_policy`.
  - An archive written before VUser iteration identity existed is
    `unavailable` with `archive_lacks_vuser_iteration_identity`.
  - A pin whose digest no longer matches the stored revision is refused
    (409), as is a run whose archive is not finalized.
- **CLI:** `apexctl analysis journeys --run ID [--policy ID [--revision N]]`.
- **Dashboard:** the run report's Performance tab has a Journey risk panel.
  It shows the per-journey counts, the indexes, up to five example attempts
  per journey (group, VUser, iteration, first failed step) and the report's
  notes. You can apply another policy revision from the panel.

Each journey's report lists its counts, eligible attempts, steps and step
failures, failure rate and example attempts. The report also names the
policy revision (`pinned` or `requested`) and its digest, and counts the
rows outside iterations (init, teardown, or no identity).

## Patterns and heatmaps

`GET /api/v1/tests/{id}/archive/browser-interactions` reads a finalized run's
interaction evidence (historical-only `browser_interaction_patterns.v2`,
`browser_interaction_patterns.v3` when raw v2/v3 is present, or
`browser_interaction_patterns.v4` when raw v4 interaction evidence is present;
append `?format=csv` for a spreadsheet-safe CSV export of coverage, patterns,
heatmaps, keyboard targets, limitations, and notes). Viewer access is
enough. The run report shows the result in its [Browser area](#the-browser-area-of-a-run-report), under **Interaction patterns**. A run without interaction evidence answers
`not_captured`; a run that captured some but kept none in its archive (a
failed transfer, a refused or torn sidecar) answers `unavailable` with what
was lost. The response states its thresholds and coverage: interactions
(`interactions` and `streamed_interactions`), pattern-sample retention
(`pattern_retained` and `pattern_truncated` when the 100,000-row pattern sample
is exceeded while streaming coverage and heatmaps continue across all records),
records, dropped (by capture bounds, or lost before the archive) and
incomplete evidence (the archive's own account, which includes sources that
never arrived), counts by provenance, gestures mapped (`mapped`, plus
`mapped_via_transform` when same-origin child-frame gestures are mapped via an
invertible 2D frame-chain transform in v4),
and gestures left out by reason. A run without an archive answers `404` (on
an HA controller, also until the finalized archive is published, so the run
report offers to check again), an archive not yet finalized `409`, and
unreadable evidence or an archive that cannot be read `500`. Nothing is
replayed or injected.

V3 and V4 pattern eligibility requires a valid authored step, engine invocation and
joined native dispatch timestamp whose source/document/lifecycle tuple is present
in the verified document archive. A separate 100,000-tuple bound limits this
join index; excess tuples remain `document_lifecycle_unavailable`, with
`document_joins_truncated` disclosed. Metric revisions do not consume extra
join entries. Its coverage counts eligible gestures and
exclusions separately (`pattern_eligible`, `pattern_excluded`), including
legacy identity, missing execution and unavailable dispatch. These counts
cover the streamed input; the existing 100,000-row retained pattern bound
still applies. Historical rows remain downloadable and may contribute only
to separately labelled historical maps in a mixed archive.

V3 and V4 group patterns by source generation, document and lifecycle and sort by
native dispatch. Repetition requires the same structural step, compatible
scroll/layout/viewport geometry and the 2-second native-event window;
duplicate display names cannot merge. Intended retries share the engine's
logical invocation and increasing attempt numbers (even when an intermediate
attempt was dropped), rather than being
inferred from proximity. Retry attempts stay out of repetition even when an
earlier attempt is missing. Pattern windows use the observed dispatch clock,
so post-action waits do not move them. Pattern and report output retain the
authored step and script identity. In V4, every missed declared page-response
outcome (`sync`, `dialog`, `download`, `assertion`, or `navigation`) is
evaluated with its own deadline and emits a distinct `dead_interaction` or
`unexpected_navigation` pattern ID; canceled
or otherwise unevaluated waits cannot become dead interactions.

Historical-only v2 analysis keeps the following rules per document, in capture order:

| Pattern | Rule |
| --- | --- |
| `scripted_repetition` | At least 3 clicks, taps or checks (a double click is one gesture; hovers, drags, swipes and long presses do not count) within 2 s, on one route and layout, when any of them is scripted or of unknown provenance. Gestures with dispatched points repeat within 32 CSS px of the first; gestures without points (touch gestures are dispatched synthetically) repeat when they share an action name and gesture kind, and their target's role and label when one is recorded. The 2 s window is measured on capture time, taken after each gesture's wait and page reads (E3 records no dispatch time), so gestures dispatched closer together than their waits may not count. |
| `repeated_gesture` | The same, when every gesture was recorded with consent. It is the only pattern that can suggest human frustration. |
| `retry` | A failed gesture followed at once by further attempts of the same step (its action name and gesture kind: each engine retry is a new execution with its own action ID), up to the first that succeeded. Retries never count as repetition. Distinct steps that share an action name and kind are one step here. |
| `dead_interaction` | A gesture whose authored outcome (its post-action `sync`) asserts that the page responded (`element_visible`, `load` or `domcontentloaded`) observed as `missed` within its deadline. A missed `dom_stable`, `network_idle` or pause says nothing about whether the page responded, and an unchanged page alone is never one. Authored assertion, dialog and download outcomes are not used. |
| `unexpected_navigation` | A gesture that navigated away while its declared `element_visible` outcome was `missed`: the element the step waited for never appeared. Navigating is otherwise normal (a recorded step waits for the next page's element, met), and a `dom_stable` or pause outcome says nothing about which page the gesture should leave. A gesture that navigated away is never a dead interaction. |

Each pattern lists its route, layout, action, count, window and up to 16
interaction IDs.

Heatmaps place each pointer gesture's first dispatched point on an abstract
64×64 grid of its viewport. Version 2 keeps separate maps for each sanitized
route, layout revision, viewport size, device pixel ratio and orientation,
engine, device profile and exact recorded scroll offset on both axes. Signed
zero offsets are equivalent; fractional offsets are not rounded together.
The coordinate space remains `viewport_css_px`. Layout, viewport and scroll
are read **after** the gesture and its wait, so these maps do not establish
dispatch-time geometry. V3 adds a separate `native_dispatch` map population
using the event-time geometry in raw v2; historical maps are labelled
`post_action`. Those populations never merge even when all other keys match.
Raw v2 without observed dispatch is excluded from maps as
`dispatch_unavailable`. Native touch emulation and untransformed child
frames remain unavailable; this does not qualify actual devices.

Engine and profile come from the gesture's source generation in the run's
verified document evidence, read in full. An empty profile on a known context
means an unprofiled browser, not an unknown context. Missing or conflicting
context evidence excludes the gesture from maps with `engine_profile_unknown`
and an explanatory limitation; it never combines unknown engines or profiles.
Unreadable document evidence (`document_evidence_unreadable`) and contexts
past the first 65,536 (`document_evidence_truncated`) have the same exclusion.
Keyboard and form gestures fill a per-map table of target roles and hashed
labels, never positions.

Some gestures are counted, by reason, instead of mapped:

- those with an unavailable reason (`child_frame_unobserved`,
  `document_changed`, `synthetic_dispatch`, `page_state_unavailable`);
- child-frame gestures without an observed transform, even if capture omitted
  an unavailable reason;
- those without a layout revision (`layout_unknown`) or a consistent engine/profile cohort;
- those without points or without a viewport;
- those outside the viewport (they make no map);
- those requiring a new map beyond the 64-map limit (`heatmaps_truncated`).

Each gesture contributes once to mapped or excluded coverage. Existing maps
still accept compatible gestures after the map limit. Raw version-1 interaction
archives remain unchanged and readable; newly computed analysis reports version
2. Previously exported version-1 analyses keep their original interpretation.
Pattern detection is unchanged in version 2, including its capture-time window
and action-name identity limitations described above.

At most 200 patterns are kept, and patterns are found among the first 100,000
interactions (`interactions_truncated` beyond that). Screenshot overlays are
not produced. Incomplete journeys are E5's journey accounting
(`/archive/journeys`), not a pattern here.

## Promoting browser intelligence support

Passing `BI-*` acceptance records require reviewed, currently accepted `BE-4`
evidence for the exact claimed measurement cohort. A pending request or a test
suite that ran earlier does not authorize promotion. This gate changes support
claims; it does not rewrite historical findings or promote preview results.

Supply a version-1 `qualification` object with the measured `runtime_sha256`,
`capability_sha256`, `metric` and `measurement_algorithm`. The acceptance record
must also identify the engine in `provider`, a nonempty profile (use `desktop`
for desktop), platform, full commit SHA and tested binary SHA-256. A BI record
adds its versioned `analysis_algorithm` and one to eight `dependencies`, each
with the accepted BE record's `record_id` and returned `record_sha256`.
Both records must name the same binary and measurement scope. Artifact hashes
remain required, and the accepted BE record's digest binds its artifact hashes,
reviewer identity, sequence and cohort. The server returns `record_sha256` so
callers do not need to reproduce its canonical serialization.

The dependency check and promotion insert share a database transaction and
writer fence. A missing, pending, failed, revoked, superseded or mismatched BE
record returns a conflict without inserting a promotion. A later withdrawal
also makes current BI support unavailable; the original accepted record and
its pins stay readable. Old records without qualification remain historical
records and cannot qualify a new BI promotion.

The returned `cohort_sha256` identifies one metric/measurement/analysis scope,
including its runtime and capability digests. Different metrics or estimators
remain separate acceptance subjects. A broader withdrawal without qualification
covers every matching cohort. Query the exact cohort in release automation:

```bash
apexctl qa record-acceptance --requirement BI-1 --feature browser-findings \
  --provider chromium --profile desktop --platform linux-amd64 \
  --commit FULL_COMMIT_SHA --binary ./controller --suite browser-intelligence \
  --result passed --artifact reviewed-evidence.json --qualification cohort.json
apexctl qa acceptance --requirement BI-1 --feature browser-findings \
  --provider chromium --profile desktop --platform linux-amd64 \
  --commit FULL_COMMIT_SHA --binary ./controller --cohort COHORT_SHA256 \
  --require-supported
```

`cohort.json` holds the qualification object only and is limited to 16 KiB.
The server validates its fields and dependency pins. Evidence bytes are not
uploaded by these commands; the existing operator review remains responsible
for the evidence behind their hashes. A passing local/emulated cohort cannot
qualify another host, actual device, engine or binary.

### Per-metric baseline effect floors

Browser policies can set separate LCP, FCP and INP minimum changes in
`effect_floors.timing_by_metric_ms`. Each optional value must be between 50 and
600,000 milliseconds. The policy editor offers the same three fields; leaving
one empty uses the overall timing floors. These values affect baseline anomaly
qualification independently of whether the metric contributes to the weighted
threshold score. CLS continues to use its separate `effect_floors.cls` field.

For each timing metric, the effective floor is the largest of its configured
metric floor, the overall absolute floor (at least 50 ms), and the overall
relative floor (at least 10% of the baseline mean). A per-metric value cannot
lower either overall floor. Runs keep the floors from their pinned immutable
policy revision even when the policy is edited later. This does not change the
`browser_baseline.v1` estimator or implement compatible-run selection.

### Original authored source identity

New managed browser runs and stored-script replays bind authored steps before
controller runtime overrides. Each version-2 `authored_step` identifies the
original script SHA-256, its structural path/type digest, the exact step path
and a derived step ID. Replay events expose that definition identity, and run
archives retain the source descriptors in `provenance.browser_authored_sources`.
The step identity does not prove that a gesture dispatched; use the separately
retained native dispatch and invocation evidence for that claim.

Version 1 continues to identify the exact engine input. It is not relabeled as
original-source proof. A restored compiled run preserves its stored source
binding; rebuilding legacy runtime inputs from the current script library leaves
original-source identity unavailable. The runtime validates the structural
binding before using a descriptor. Public DSL and replay option JSON cannot
supply source authority.

Prepared generator assignments carry source descriptors only after fresh
`browser_original_source_v1` capability negotiation and require an exact
staging acknowledgement. Older or stale peers and immediate assignments retain
legacy identity. Source equality alone does not establish matching browser
runtime, policy, load, document, page or measurement cohorts. Authored page
identity and compatible-baseline selection remain separate requirements.


## Choose policy revisions for a run or recurring schedule

In New run (both **Run setup** and **Schedules**), expand **Browser and journey policies** to choose an exact browser or journey policy revision. The latest revision is offered initially; an older revision remains selectable by its revision number. Leaving a kind on **Project default at start** preserves default resolution. Policy lists load when the section opens.

Run drafts, held runs, delayed starts, revised queued copies and recurring scenario schedules (`scenario_schedules.analysis_policy_selections_json`, schema v85) retain the selection. In a revised copy or a saved recurring schedule edit, explicitly choosing the project default (`analysis_policy_selections: {}`) clears the inherited or previously saved selection; omitting `analysis_policy_selections` on a partial schedule update (such as pausing or enabling) preserves the saved selection. Switching projects clears the prior project's selection. Saved schedules display their pinned revision tokens (`browser policy rN`, `journey policy rN`) in the scenario schedule table and project schedule catalog.

Creation and schedule preview/save check the selected revision and project. Admission and recurring schedule materialization resolve the same immutable revision again and compute its digest on the server. A later edit does not change the chosen revision. A policy deletion visible when admission or recurring schedule materialization resolves the selection refuses the start (recording `reason_code: "analysis_policy_unavailable"` on the failed schedule occurrence); it cannot silently replace the selection with a default. Already admitted runs retain their historical policy pins and findings. Policy resolution and the admission write use the existing run-identity and execution-ownership fences.

The archive's `provenance.policy_pins` identifies an explicit selection with `source: run_selection`. A default uses `source: project_default`. The selected revision and current tombstone are read in one database snapshot. A concurrent deletion after that read follows the same admission boundary as existing default policies. Historical policy reads remain available after deletion for authorized inspection.


## Observed post-action wait clocks

Raw browser interaction evidence version 3 retains a supported Sync wait's actual host-side start, effective deadline, observed return and monotonic elapsed duration. These clocks are separate from native gesture dispatch and later diagnostic capture. After-action delay or cancellation during capture cannot rewrite a result already observed at the wait boundary. A parent context may shorten the effective deadline; cancellation, invalid or missing clocks, and a success first observed after its deadline do not establish a satisfied authored deadline.

The controller negotiates `browser_interaction_evidence_v3` with a fresh generator heartbeat and an exact traffic-free staging acknowledgement. Older generators keep their negotiated version-2 evidence. Historical version-1/version-2 records never receive invented clocks. The raw evidence version is separate from interaction analysis version 3; repeat and retry windows continue to use native dispatch time, with unchanged population exclusions.

This receipt covers the runtime's declared post-action Sync waits. It does not claim composition of assertion, dialog, download, navigation and state outcomes, authored page identity, actual-device qualification or an independent target clock. Retained raw evidence preserves clock and outcome together through archive serialization. Browser and mobile-emulation cohorts retain their existing native-versus-synthetic dispatch limitations.


## Separate browser cohorts in saved panels

For a completed run, open **Performance** in its report. In Create project analytics panel, choose Archived browser documents and enable **Show separate browser cohorts**. Set a maximum of 1–20 series and retain exact engine, profile, frame, lifecycle, group, region or cohort filters when needed. A selection exceeding the limit fails explicitly. Each tuple has at most 1,000 time buckets; the combined result has at most 20,000 buckets.

These panels save query version 3 with `browser_tuple_mode: separate`. Their versioned result keeps each complete measurement tuple separate, with its own runtime cohort digest, final-document counts, missing buckets and CSV export. Progress snapshots and excluded/censored measurements never enter the percentiles. The legacy bucket array is empty, so an older consumer cannot mistake the result for a pooled distribution. Queries saved before this option retain their single-tuple behavior.

A comparison pairs exact tuple identities and reports missing current/baseline tuples or unavailable workload/coverage compatibility. It does not produce an overall pooled verdict or establish a baseline anomaly. Derived multi-tuple calculations remain unavailable; select one exact tuple to use the existing derivations. Saved comparison panels still resolve their input runs through the existing project authorization; immutable comparison-to-panel input pinning remains separate work.


## Supported authored navigation page receipts

Archived document evidence includes a separate **Authored browser pages** view when the runtime retained page-binding receipts. A receipt joins the original structural navigation step to the response actually returned by that command and the observed native document/lifecycle. Repeated executions of that step share an authored page ID while retaining distinct runtime document IDs; duplicate display names do not merge structural steps. Final document measurements are not rewritten when the navigation command later returns.

Version 1 supports explicit Chromium navigation/reload/new-window responses whose existing CDP loader join proves the document. Version 2 supports Chromium and WebKit when the exact native navigation request and sequence remain unchanged around a bounded document/lifecycle identity observation. Canceled reads, stale requests or source generations cannot acquire a binding. Redirects rely on the returned native request rather than URL equality. History/BFCache, same-document/SPA and click-created popup cases without that proof remain unavailable; no page ID is guessed for them or for historical records. Binding a page does not establish equivalent runtime state, journey success, baseline compatibility or actual-device qualification.

The report retains at most 1,024 distinct document/lifecycle bindings, reports malformed/source-mismatched/conflicting and dropped receipts, and never readmits a conflicting identity within that retained set. Counts describe retained receipts, not every page issued. The dashboard displays at most 256 bindings; CSV exports all retained bindings. Original source/structure digests, structural step, native request, source generation and document/lifecycle remain inspectable.


## Reading native waterfall and cache evidence

The retained resource inspector draws request phases only when a native document join and document-relative start are available. A missing join leaves the waterfall unavailable; URLs and action timestamps cannot fill it in. Request phases are not cross-host clock synchronization or server-processing measurements.

Each response has its own cache availability. Chromium can expose an observed cache hit; a cached response must not claim a negotiated network protocol. WebKit may expose neither a cache decision nor a document join, even when an independent fixture can count requests. The inspector preserves these unavailable states. At narrow widths, scroll the retained resource table horizontally to reach the inspection column.

Controlled local fixtures reconcile the rendered native waterfall with server receive/finish observations and native HAR timing. They use absolute navigation URLs without a script base URL so the controller's separate HTTP latency probe is not configured. These fixtures qualify only their recorded host/engine/emulation cohorts, not actual devices, remote clock synchronization or reference-host performance.


## Frozen browser comparison inputs

On a completed run's **Performance** report, enable **Show separate browser cohorts** and **Freeze archive inputs for this panel** in the project panel editor. Optionally select a comparison run. Save evaluates the authorized archives and retains the exact current/baseline run IDs, verified publication digests, complete tuple identities and query digest. Query version 4 is explicit; existing panels keep their prior semantics.

A frozen panel opened from another run continues reading its saved inputs. Changed, deleted, expired or inaccessible publications remain unavailable; they never silently switch to that other run or the latest archive. Preview and explicitly save new inputs to select a replacement. The frozen-input details and CSV expose publication/query provenance. Baseline-only cohorts retain their measured buckets and CSV rows; absent current values and counts stay missing instead of becoming zero. These pins preserve the archive's recorded policy and cohort evidence; they do not establish baseline anomaly eligibility or a transaction spanning independent archive publications.

Editors may save at most 20 frozen panels together, within the shared analysis admission and deadline. Each input retains its existing limits; the combined current/baseline panel also enforces the selected tuple limit (at most 20) and 20,000 time-bucket rows, including baseline-only cohorts. An oversized union is rejected explicitly. Viewers may inspect authorized saved panels and CSV; native HAR downloads retain their separate authorization. Metadata-only renaming preserves historical pins even when an input has expired. Changing a saved query requires evaluation of its actual inputs.
