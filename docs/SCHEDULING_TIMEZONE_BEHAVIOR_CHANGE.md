# Behavior change: scenario schedules now fire in their declared timezone

> Change record (kept for history): documents a past behavior change and is not a living specification; see the scheduling docs and code for current behavior.

## What changed

Previously, a scenario schedule's cron expression was evaluated in **UTC**, even
when the schedule declared a `timezone` (e.g. `America/Chicago`). The `timezone`
field was stored and honored for blackout / maintenance / availability windows,
but **not** for the cron pattern itself — so a `0 9 * * *` schedule in
`America/Chicago` fired at **09:00 UTC** (03:00/04:00 local), not 09:00 local.

As of this release, the cron expression is matched in the schedule's declared
timezone:

- `next_run_at` is still **stored in UTC**, but it now corresponds to the correct
  local wall-clock time.
- DST is handled correctly: a time that does not exist on a spring-forward day
  (e.g. `30 2 * * *` America/Chicago on the second Sunday of March) is skipped to
  the next valid day; on a fall-back day the **earliest** matching instant wins.
- An empty or unparseable `timezone` continues to evaluate in UTC.

This release also fixes POSIX day-of-month / day-of-week semantics: when **both**
the day-of-month and day-of-week fields are restricted (neither is `*`), a run
fires when **either** matches (previously it required both). For example,
`0 9 15 * 1` now fires at 09:00 on the 15th **or** any Monday.

## Operator impact / migration

- **Existing schedules with a non-UTC timezone will shift** from firing at their
  old UTC time to firing at their declared local time. No data migration is
  required: a schedule re-computes its `next_run_at` with the corrected logic on
  its next materialization (or whenever its cron/timezone is next edited).
- During the transition, a schedule's **already-stored** `next_run_at` (computed
  under the old UTC logic) is used for its first post-upgrade firing; subsequent
  firings use the corrected local-time logic. Edit and re-save a schedule to
  recompute `next_run_at` immediately if you need the corrected time right away.
- Schedules declared in UTC (the default) are unaffected.

## Dashboard

The run calendar (Monitoring → Schedules) now renders event times in each event's
declared timezone with a short zone label, instead of the browser's local zone.
