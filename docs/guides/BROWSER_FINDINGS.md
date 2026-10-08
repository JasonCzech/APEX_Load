# Browser findings and qualified scores

Browser findings judge a run's archived document metrics against a
versioned `browser_policy`, server side. They produce two things:

- versioned finding records (`browser_findings.v1`);
- a weighted score (`browser_score.v1`).

Only qualified final observations count. The dashboard's legacy summary
estimate (`legacy-summary-availability-v2`) stays for runs that have no
server result.

## Policy

A `browser_policy` (Analysis policies, `POST /api/v1/analysis/policies`)
contains:

- **Metrics:** up to four of `fcp`, `lcp`, `cls` and `inp`. Each has a
  `good` and a `poor` threshold (`0 <= good < poor`) and a non-negative
  `weight`. A metric with a positive weight is **required** for an overall
  score.
- **Applicability:** optional engine (`chromium`, `webkit`) and device
  profile lists. A cohort outside them is suppressed, never scored.
- **Effect floors and baseline mode:** these are used by baseline anomalies
  (a separate rule set). A threshold finding records the baseline mode with
  status `not_evaluated`.

A policy marked `apply_by_default` is pinned to every run started in the
project, by revision and digest.

## When evaluation runs

Evaluation is a `browser_intelligence` evaluation job.

- **Automatically:** when a run's archive is finalized, if the run pinned a
  browser policy and requested document evidence. An evaluation of the same
  inputs (same archive digest and policy revision) is never queued twice.
- **On demand:**
  - `POST /api/v1/analysis/evaluations` with
    `{"kind":"browser_intelligence","request":{"run":"<id>"}}`. Add
    `"policy":{"id":…,"revision":…}` for another revision.
  - The Evaluate button on the run report's Browser findings panel.
  - `apexctl analysis browser-findings --run ID --evaluate --project P [--wait]`.

The job's input fingerprint covers the rule set, the score version, the
policy revision's digest and the archive manifest's digest.

## What is judged

Only main-frame navigations are judged. Child frames and back/forward-cache
restores are listed in the result's notes, never scored.

Each cohort (engine and device profile) is judged per policy metric. The
observed value is the p75 of the qualified final observations. It is
approximate: the upper bound of its distribution bucket, capped by the
observed maximum.

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

## Score

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

## Reading results

- **API:** `GET /api/v1/tests/{id}/archive/browser-findings` returns the
  run's latest ready result (`status: ready`), `pending`, or
  `not_evaluated`.
- **Dashboard:** the Browser findings panel in the run report's Diagnostics
  tab (Browser evidence) shows the score, its components and contributions,
  the findings by state, and the notes.
- **Webhooks:** subscribe to `browser_finding` and `browser_finding_resolved`.
  When an evaluation becomes ready:
  - a finding active now but not in the run's previous ready evaluation is
    sent once;
  - one active before and not now is sent as resolved;
  - an identical rerun sends nothing;
  - at most 20 events are sent per evaluation.
