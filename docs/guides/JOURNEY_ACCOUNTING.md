# Journey accounting

Journey accounting reads a finished run's archived samples and counts how its
declared journeys turned out. The result is weighted test-journey risk under
a versioned policy. It is never revenue or conversion impact, which would
need an authorized external model.

## Declare journeys

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

## How attempts are classified

Every archived sample carries its VUser and 1-based iteration. Each VUser
iteration is read in time order. Samples are archived as they are recorded,
so a transaction's row follows its own steps. Ordering by time puts it back
around them:

- A start boundary opens an attempt when it begins.
- Action samples that begin while an attempt is open are its steps.
- The success boundary decides the attempt when it ends.

| Outcome | Meaning |
| --- | --- |
| completed | The success boundary succeeded. Failed steps before it are still counted as step failures. |
| failed | The success boundary failed, or the iteration ended after one of the attempt's steps failed. One failed journey counts once, however many steps failed. |
| aborted | The iteration ended, or the journey started again, without the outcome and without a failed step. |
| censored | The outcome is unknown, because the run (or a generator's share of it) ended while the attempt was open, or the stop cut it. |
| skipped | An iteration of a group that ran the journey somewhere never started it. A possibly cut iteration does not count. |

Eligible attempts are the completed, failed and aborted ones. Censored and
skipped attempts never enter the index.

### Stops and interrupted samples

The engine marks a failure `interrupted` when it was recorded after the VUser
was told to stop. That covers two cases:

- The action in flight was cancelled by a hard stop (or by Run's own context
  when there is no hard-stop context).
- A stop or goal boundary abandoned the iteration and left a failed
  transaction behind.

Cancellation alone cannot establish a failure. An interrupted failure
therefore censors every attempt it cut and is not counted as a step failure.
An attempt that still reaches its outcome keeps it.

The archive cannot tell an iteration the run's end cut short from one that
simply ended. So an attempt still open in a VUser's last iteration is
censored rather than aborted or failed. In an iteration-limited run this is
conservative: an attempt that genuinely stopped short in the final iteration
is censored, not aborted.

A replacement generator that continues an evacuated VUser restarts its
iterations. The earlier share's last iteration is treated as possibly cut.

## Indexes

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

Past any bound the report is `incomplete`, and both indexes are withheld
(`accounting_incomplete`).

## Read the result

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
