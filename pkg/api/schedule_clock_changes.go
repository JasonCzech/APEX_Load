package api

import (
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// ScheduleClockChange is a daylight-saving change in a schedule's timezone
// that the schedule's wall-clock times fall into. When the clocks go forward
// the matched local times do not exist, so those runs are skipped; when they
// go back the matched times happen twice and run once, at the first. The
// evaluator (nextCronRun) already behaves this way; this names it.
type ScheduleClockChange struct {
	// Date is the local calendar date of the affected times (YYYY-MM-DD).
	Date string `json:"date"`
	// Kind is "skipped" (clocks go forward) or "repeated" (clocks go back).
	Kind string `json:"kind"`
	// LocalTimes are the first few matched wall-clock times (HH:MM).
	LocalTimes []string `json:"local_times"`
	// Matches counts every matched wall-clock time of the change.
	Matches int `json:"matches"`
	// RunsAt is, for repeated times, the one instant the first of them runs.
	RunsAt *time.Time `json:"runs_at,omitempty"`
	// At is the instant the clocks change.
	At time.Time `json:"at"`
}

const (
	maxScheduleClockChanges       = 6
	maxScheduleClockChangeTimes   = 3
	maxScheduleClockTransitions   = 64
	scheduleClockChangeMinHorizon = 366 * 24 * time.Hour
)

// scheduleClockChangeHorizon is how far ahead clock changes are named: at
// least a year, and at least to the given run, so a run pushed past a skipped
// date (a yearly 02:30 on a spring-forward day) is explained.
func scheduleClockChangeHorizon(from time.Time, through *time.Time) time.Time {
	until := from.Add(scheduleClockChangeMinHorizon)
	if through != nil && through.After(until) {
		return through.UTC()
	}
	return until
}

// scheduleClockChanges lists the clock changes in timezone between from and
// until that cronExpr's wall-clock times fall into, at most
// maxScheduleClockChanges. allows, when set, is the schedule's own gating
// (pauses and windows): a time it would not run anyway is not named.
func scheduleClockChanges(cronExpr, timezone string, from, until time.Time, allows func(time.Time) (bool, error)) []ScheduleClockChange {
	location := scheduleEvalLocation(timezone)
	if location == time.UTC || validateCronExpr(cronExpr) != nil {
		return nil
	}
	fields := strings.Fields(expandCronMacro(cronExpr))
	if len(fields) != 5 {
		return nil
	}
	fields[3] = applyCronAliases(fields[3], cronMonthAliases)
	fields[4] = applyCronDowAliases(fields[4])
	matches := func(wall time.Time) bool {
		return cronFieldMatches(fields[3], int(wall.Month()), 1, 12) &&
			cronMatchesDayOfMonthAndWeek(fields[2], fields[4], wall) &&
			cronFieldMatches(fields[1], wall.Hour(), 0, 23) &&
			cronFieldMatches(fields[0], wall.Minute(), 0, 59)
	}
	allowed := func(at time.Time) bool {
		if allows == nil {
			return true
		}
		ok, err := allows(at)
		return err != nil || ok
	}
	var changes []ScheduleClockChange
	cursor := from.UTC()
	for transitions := 0; transitions < maxScheduleClockTransitions && len(changes) < maxScheduleClockChanges; transitions++ {
		_, end := cursor.In(location).ZoneBounds()
		if end.IsZero() || end.After(until) || !end.After(cursor) {
			break
		}
		cursor = end
		_, before := end.Add(-time.Nanosecond).In(location).Zone()
		_, after := end.In(location).Zone()
		delta := after - before
		if delta == 0 {
			continue
		}
		change := ScheduleClockChange{At: end.UTC(), Kind: "skipped"}
		span := time.Duration(delta) * time.Second
		// The wall clock the change jumps over, read on the side that still
		// shows it.
		wallStart := end.In(time.FixedZone("", before))
		if delta < 0 {
			change.Kind = "repeated"
			span = -span
			wallStart = end.In(location)
		}
		change.Date = wallStart.Format(time.DateOnly)
		for offset := time.Duration(0); offset < span; offset += time.Minute {
			wall := wallStart.Add(offset)
			if !matches(wall) {
				continue
			}
			// A skipped time has no instant of its own: it would have run at
			// the change. A repeated one runs at its first occurrence.
			at := end
			if delta < 0 {
				at = end.Add(-span).Add(offset)
			}
			if !allowed(at) {
				continue
			}
			change.Matches++
			if len(change.LocalTimes) < maxScheduleClockChangeTimes {
				change.LocalTimes = append(change.LocalTimes, wall.Format("15:04"))
			}
			if delta < 0 && change.RunsAt == nil {
				first := at.UTC()
				change.RunsAt = &first
			}
		}
		if change.Matches > 0 {
			changes = append(changes, change)
		}
	}
	return changes
}

// scenarioScheduleClockChanges names the clock changes each saved scenario
// schedule falls into, keyed by schedule ID; schedules without any are left
// out.
func scenarioScheduleClockChanges(schedules []*storage.ScenarioSchedule, now time.Time) map[string][]ScheduleClockChange {
	byID := map[string][]ScheduleClockChange{}
	for _, schedule := range schedules {
		if schedule == nil {
			continue
		}
		changes := scheduleClockChanges(schedule.CronExpr, schedule.Timezone, now, scheduleClockChangeHorizon(now, schedule.NextRunAt), func(candidate time.Time) (bool, error) {
			return scheduleAllowsCandidate(schedule, candidate)
		})
		if len(changes) > 0 {
			byID[schedule.ID] = changes
		}
	}
	return byID
}

// executionPlanScheduleClockChanges is scenarioScheduleClockChanges for
// execution-plan schedules.
func executionPlanScheduleClockChanges(schedules []*storage.ExecutionPlanSchedule, now time.Time) map[string][]ScheduleClockChange {
	byID := map[string][]ScheduleClockChange{}
	for _, schedule := range schedules {
		if schedule == nil {
			continue
		}
		changes := scheduleClockChanges(schedule.CronExpr, schedule.Timezone, now, scheduleClockChangeHorizon(now, schedule.NextRunAt), executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
		if len(changes) > 0 {
			byID[schedule.ID] = changes
		}
	}
	return byID
}
