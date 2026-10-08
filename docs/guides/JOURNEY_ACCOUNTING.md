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

### Stops and interrupted samples

The engine marks a failure `interrupted` when it was recorded after the VUser
was told to stop, or when the load generator itself refused it. That covers
four cases:

- The action in flight was cancelled by a hard stop (or by Run's own context
  when there is no hard-stop context).
- A stop or goal boundary abandoned the iteration and left a failed
  transaction behind.
- A transaction the VUser still had open when a stop ended it (the run's
  stop or end, a ramp-down or scale-down, a goal boundary).
- A resource-limit failure (the error class `resource_limit`, for example a
  browser context closed at its HAR or memory limit): the generator, not the
  system under test, ended the action.

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

The report is also incomplete when the archive scan stops at its budget
(2,000,000 rows or 512 MiB), when a distributed run's generator sources were
not all collected, or when rows from a source without VUser identity (an
older generator) sit beside rows with it. An incomplete report lists its
`incomplete_reasons`, and both indexes are withheld
(`accounting_incomplete`). A run that recorded identity but ran no iteration
is `unavailable` with `no_iterations` (as is an archive with no samples at
all).

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
