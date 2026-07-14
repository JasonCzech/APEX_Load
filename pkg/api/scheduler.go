package api

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioSchedulePersistence interface {
	SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error
	GetScenarioSchedule(id string) (*storage.ScenarioSchedule, error)
	ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, error)
	ListAllScenarioSchedules() ([]*storage.ScenarioSchedule, error)
	DeleteScenarioSchedule(id string) error
	SaveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) error
	GetScenarioScheduleOccurrence(id string) (*storage.ScenarioScheduleOccurrence, error)
	GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*storage.ScenarioScheduleOccurrence, error)
	ListScenarioScheduleOccurrences(scheduleID string) ([]*storage.ScenarioScheduleOccurrence, error)
	ListAllScenarioScheduleOccurrences() ([]*storage.ScenarioScheduleOccurrence, error)
	SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*storage.SchedulerLease, error)
	SaveAuditEvent(event *storage.AuditEvent) error
}

type latestScenarioScheduleOccurrenceStore interface {
	LatestScenarioScheduleOccurrence(scheduleID string) (*storage.ScenarioScheduleOccurrence, error)
}

type atomicScenarioScheduleApprovalStore interface {
	ResolveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval, schedule *storage.ScenarioSchedule, expectedScheduleRevision int) (bool, error)
}

type atomicScenarioScheduleApprovalWriteStore interface {
	SaveScenarioScheduleWithApproval(schedule *storage.ScenarioSchedule, approval *storage.ScenarioScheduleApproval, expectedRevision int) (bool, error)
}

type scenarioScheduler struct {
	runner   *TestRunner
	now      func() time.Time
	ownerID  string
	leaseTTL time.Duration
}

const maxScheduleCatchUpAllPerSweep = 25

type CreateScenarioScheduleRequest struct {
	Name                string                           `json:"name"`
	CronExpr            string                           `json:"cron_expr"`
	Timezone            string                           `json:"timezone,omitempty"`
	ReleaseID           string                           `json:"release_id,omitempty"`
	TimeslotID          string                           `json:"timeslot_id,omitempty"`
	RunNameTemplate     string                           `json:"run_name_template,omitempty"`
	Enabled             *bool                            `json:"enabled,omitempty"`
	ApprovalPolicy      string                           `json:"approval_policy,omitempty"`
	MissedRunPolicy     string                           `json:"missed_run_policy,omitempty"`
	BlackoutWindows     []storage.ScenarioScheduleWindow `json:"blackout_windows,omitempty"`
	MaintenanceWindows  []storage.ScenarioScheduleWindow `json:"maintenance_windows,omitempty"`
	AvailabilityWindows []storage.ScenarioScheduleWindow `json:"availability_windows,omitempty"`
	// DisabledFrom/DisabledUntil pause the schedule over an absolute date range
	// without disabling it; either bound may be nil for an open-ended pause.
	DisabledFrom     *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil    *time.Time `json:"disabled_until,omitempty"`
	OwnerSubject     string     `json:"-"`
	OwnerDisplayName string     `json:"-"`
	RBACReviewSource string     `json:"-"`
}

type ScenarioScheduleHealth struct {
	Total           int            `json:"total"`
	Enabled         int            `json:"enabled"`
	Due             int            `json:"due"`
	Blocked         int            `json:"blocked"`
	Failed          int            `json:"failed"`
	PendingApproval int            `json:"pending_approval"`
	Queued          int            `json:"queued"`
	Materialized    int            `json:"materialized"`
	Skipped         int            `json:"skipped"`
	Completed       int            `json:"completed"`
	StatusCounts    map[string]int `json:"status_counts"`
}

func newScenarioScheduler(runner *TestRunner) *scenarioScheduler {
	return &scenarioScheduler{runner: runner, now: time.Now, ownerID: uuid.New().String(), leaseTTL: 30 * time.Second}
}

func (s *scenarioScheduler) RunDueSchedules() error {
	if s == nil || s.runner == nil {
		return nil
	}
	// Materializing a due schedule starts a run, so scheduling is leader-only.
	// This keeps scheduling on the same replica that executes; the dedicated
	// scenario-scheduler lease below still guards against duplicate sweeps.
	if !s.runner.isExecutionLeader() {
		return nil
	}
	// Schedule/scenario CRUD may be served by any HA replica. Refresh the
	// leader's durable view before each sweep so follower writes and deletes are
	// observed without requiring another leadership transition.
	if s.runner.leadership != nil {
		if err := s.runner.reloadStoredScripts(); err != nil {
			return fmt.Errorf("reload scripts before schedule sweep: %w", err)
		}
		if err := s.runner.reloadStoredScenarios(); err != nil {
			return fmt.Errorf("reload scenarios before schedule sweep: %w", err)
		}
	}
	now := s.now().UTC()
	acquired, err := s.runner.acquireScenarioSchedulerLease(now, s.ownerID, s.leaseTTL)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	toStart := make([]*ManagedTest, 0)
	s.runner.mu.Lock()
	for _, schedule := range s.runner.listAllScenarioSchedulesLocked() {
		if !s.runner.isExecutionLeader() {
			break
		}
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		catchUpAll := normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all"
		for materialized := 0; schedule.NextRunAt != nil && !schedule.NextRunAt.After(now); materialized++ {
			if materialized >= maxScheduleCatchUpAllPerSweep {
				schedule.NextRequiredAction = "catch_up_limit_reached"
				schedule.LastError = fmt.Sprintf("catch_up_all materialized %d occurrences; next sweep will continue", maxScheduleCatchUpAllPerSweep)
				schedule.UpdatedAt = now
				if err := s.runner.saveScenarioScheduleLocked(schedule); err != nil {
					slog.Warn("failed to persist scenario catch-up limit", "schedule_id", schedule.ID, "error", err)
				}
				break
			}
			if handled, err := s.runner.applyMissedRunPolicyLocked(schedule, now); err != nil {
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := s.runner.saveScenarioScheduleLocked(schedule); saveErr != nil {
					slog.Warn("failed to persist scenario schedule error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			} else if handled {
				break
			}
			if !s.runner.isExecutionLeader() {
				break
			}
			test, err := s.runner.runScenarioScheduleLocked(schedule, now)
			if err != nil {
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := s.runner.saveScenarioScheduleLocked(schedule); saveErr != nil {
					slog.Warn("failed to persist scenario materialization error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			}
			if test != nil {
				toStart = append(toStart, test)
			}
			if !catchUpAll || test == nil {
				break
			}
		}
	}
	dueOneTimeScheduled := s.runner.dueOneTimeScheduledTestIDsLocked(now)
	s.runner.mu.Unlock()
	if !s.runner.isExecutionLeader() {
		return nil
	}
	for _, test := range toStart {
		go s.runner.runTest(test)
	}
	for _, testID := range dueOneTimeScheduled {
		if err := s.runner.StartTestWithOptions(testID, StartTestOptions{}); err != nil {
			s.runner.markScheduledStartError(testID, err.Error())
		}
	}
	// Recurring execution-plan schedules share this leader-gated sweep + lease.
	// Run lock-free (materialize takes tr.mu internally).
	if !s.runner.isExecutionLeader() {
		return nil
	}
	s.runner.sweepDueExecutionPlanSchedules(now)
	return nil
}

func (tr *TestRunner) applyMissedRunPolicyLocked(schedule *storage.ScenarioSchedule, now time.Time) (bool, error) {
	if schedule == nil || schedule.NextRunAt == nil || now.Sub(*schedule.NextRunAt) <= time.Minute {
		return false, nil
	}
	switch normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) {
	case "catch_up_once", "catch_up_all":
		return false, nil
	case "skip":
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, *schedule.NextRunAt, "skipped", "missed_run_skipped", "Missed scheduled occurrence skipped by policy.", nil, now)
		if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
			return true, err
		}
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, err
		}
		schedule.NextRunAt = next
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.LastError = ""
		schedule.UpdatedAt = now
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
			return true, err
		}
		return true, nil
	case "require_operator_decision":
		if err := tr.blockScheduleOccurrenceLocked(schedule, *schedule.NextRunAt, "missed_run_requires_decision", "Missed scheduled occurrence requires an operator decision.", "operator_decision_required", "missed scheduled occurrence requires operator decision", now, nil); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (tr *TestRunner) acquireScenarioSchedulerLease(now time.Time, ownerID string, ttl time.Duration) (bool, error) {
	if tr == nil || tr.scenarioStore == nil {
		return true, nil
	}
	return tr.scenarioStore.TryAcquireSchedulerLease("scenario-scheduler", ownerID, now, ttl)
}

func nextScenarioScheduleRun(schedule *storage.ScenarioSchedule, from time.Time) (*time.Time, error) {
	if schedule == nil {
		return nil, fmt.Errorf("schedule is required")
	}
	return nextCronRun(schedule.CronExpr, schedule.Timezone, from, func(candidate time.Time) (bool, error) {
		return scheduleAllowsCandidate(schedule, candidate)
	})
}

// nextCronRun is the shared cron evaluator behind both scenario schedules and
// execution-plan schedules. It finds the first UTC instant strictly after `from`
// whose cron fields match in the declared timezone and that `allows` accepts
// (window/blackout/pause gating). `allows` may be nil (no extra gating).
func nextCronRun(cronExpr, timezone string, from time.Time, allows func(candidate time.Time) (bool, error)) (*time.Time, error) {
	from = from.UTC()
	if strings.TrimSpace(cronExpr) == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	fields := strings.Fields(expandCronMacro(cronExpr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields")
	}
	// Resolve 3-letter month/day-of-week aliases (and dow 7=Sunday) to numbers up
	// front so the rest of the matcher stays purely numeric.
	fields[3] = applyCronAliases(fields[3], cronMonthAliases)
	fields[4] = applyCronDowAliases(fields[4])
	location := scheduleEvalLocation(timezone)
	// Search far enough ahead to find a Feb-29 occurrence, whose gap between valid
	// dates is up to ~4 years (a leap-day schedule like "0 0 29 2 *"). A 1-year
	// window wrongly rejected those; this covers a full leap cycle plus margin.
	limit := from.Add(maxCronSearchHorizon)
	if !cronHasPossibleCalendarDate(fields[2], fields[3], fields[4], from, limit, location) {
		return nil, fmt.Errorf("could not find a matching run within %d years — check for an impossible day/month combination (e.g. Feb 30, or Feb 29 outside a leap year)", maxCronSearchHorizonYears)
	}
	for candidate := from.Truncate(time.Minute).Add(time.Minute); !candidate.After(limit); candidate = candidate.Add(time.Minute) {
		// Match cron fields in the schedule's declared timezone so a schedule
		// fires at its local wall-clock time; iteration and the returned
		// next_run_at stay in UTC. During a DST spring-forward the skipped local
		// hour simply never matches (the desired skip); during fall-back the
		// earliest matching UTC instant wins.
		local := candidate.In(location)
		if cronFieldMatches(fields[0], int(local.Minute()), 0, 59) &&
			cronFieldMatches(fields[1], int(local.Hour()), 0, 23) &&
			cronFieldMatches(fields[3], int(local.Month()), 1, 12) &&
			cronMatchesDayOfMonthAndWeek(fields[2], fields[4], local) {
			if allows != nil {
				if allowed, err := allows(candidate); err != nil {
					return nil, err
				} else if !allowed {
					continue
				}
			}
			next := candidate.UTC()
			return &next, nil
		}
	}
	return nil, fmt.Errorf("could not find a matching run within %d years — check for an impossible day/month combination (e.g. Feb 30, or Feb 29 outside a leap year)", maxCronSearchHorizonYears)
}

func cronHasPossibleCalendarDate(domField, monthField, dowField string, from time.Time, limit time.Time, location *time.Location) bool {
	dowField = strings.TrimSpace(dowField)
	if dowField != "" && dowField != "*" && dowField != "?" {
		return true
	}
	domField = strings.ToUpper(strings.TrimSpace(domField))
	if domField == "" || domField == "*" || domField == "?" || strings.ContainsAny(domField, "LW#") {
		return true
	}
	startYear := from.In(location).Year()
	endYear := limit.In(location).Year()
	for year := startYear; year <= endYear; year++ {
		for month := 1; month <= 12; month++ {
			if !cronFieldMatches(monthField, month, 1, 12) {
				continue
			}
			for day := 1; day <= 31; day++ {
				if !cronFieldMatches(domField, day, 1, 31) {
					continue
				}
				candidate := time.Date(year, time.Month(month), day, 12, 0, 0, 0, location)
				if candidate.Month() != time.Month(month) || candidate.Day() != day {
					continue
				}
				if candidate.Before(from.In(location)) || candidate.After(limit.In(location)) {
					continue
				}
				return true
			}
		}
	}
	return false
}

const (
	// maxCronSearchHorizonYears bounds the minute-by-minute next-run search. It
	// must exceed the longest gap between valid occurrences of any legal cron — a
	// Feb-29 schedule recurs only every ~4 years — so legitimate leap-day
	// schedules are not wrongly rejected.
	maxCronSearchHorizonYears = 5
	maxCronSearchHorizon      = maxCronSearchHorizonYears * 366 * 24 * time.Hour
)

var cronFieldNames = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}

// day-of-week upper bound is 7 because both 0 and 7 denote Sunday (POSIX/Quartz).
var cronFieldBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

var cronMonthAliases = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var cronDowAliases = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

var cronAlphaToken = regexp.MustCompile(`[A-Za-z]+`)

// applyCronAliases replaces 3-letter month/day-of-week alias tokens (case
// insensitive) with their numeric equivalents, leaving every other token —
// including the single-letter L/W specifiers — untouched. Run before the numeric
// matcher so ranges/lists/steps over aliases (MON-FRI, JAN,MAR) work for free.
func applyCronAliases(field string, aliases map[string]int) string {
	return cronAlphaToken.ReplaceAllStringFunc(field, func(token string) string {
		if n, ok := aliases[strings.ToLower(token)]; ok {
			return strconv.Itoa(n)
		}
		return token
	})
}

func applyCronDowAliases(field string) string {
	return cronAlphaToken.ReplaceAllStringFunc(field, func(token string) string {
		lower := strings.ToLower(token)
		if n, ok := cronDowAliases[lower]; ok {
			return strconv.Itoa(n)
		}
		if strings.HasSuffix(lower, "l") {
			if n, ok := cronDowAliases[strings.TrimSuffix(lower, "l")]; ok {
				return strconv.Itoa(n) + "L"
			}
		}
		return token
	})
}

// expandCronMacro rewrites a named macro (@daily, @weekly, …) into its 5-field
// equivalent. Non-macro expressions pass through unchanged; the raw form is what
// is persisted, so a schedule keeps showing the macro the operator entered.
func expandCronMacro(expr string) string {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@yearly", "@annually":
		return "0 0 1 1 *"
	case "@monthly":
		return "0 0 1 * *"
	case "@weekly":
		return "0 0 * * 0"
	case "@daily", "@midnight":
		return "0 0 * * *"
	case "@hourly":
		return "0 * * * *"
	default:
		return expr
	}
}

// validateCronExpr checks a 5-field cron expression and returns a specific error
// naming the offending field/token, so a rejected expression (e.g. an imported
// LoadRunner schedule using L/W/#) fails with an actionable message instead of
// the generic "could not find next run within one year".
func validateCronExpr(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("cron expression is required")
	}
	fields := strings.Fields(expandCronMacro(expr))
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 space-separated fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}
	fields[3] = applyCronAliases(fields[3], cronMonthAliases)
	fields[4] = applyCronDowAliases(fields[4])
	for i, field := range fields {
		min, max := cronFieldBounds[i][0], cronFieldBounds[i][1]
		for _, part := range strings.Split(field, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return fmt.Errorf("empty term in cron %s field %q", cronFieldNames[i], field)
			}
			if err := validateCronPart(part, min, max, cronFieldNames[i]); err != nil {
				return err
			}
		}
	}
	if err := validateCronDayMonthFeasible(fields[2], fields[3]); err != nil {
		return err
	}
	return nil
}

// singleCronNumber returns the value of a field that is a single fixed integer
// (no list/range/step/wildcard/specifier), else ok=false.
func singleCronNumber(field string) (int, bool) {
	field = strings.TrimSpace(field)
	if field == "" || strings.ContainsAny(field, ",-/*?") || strings.ContainsAny(strings.ToUpper(field), "LW#") {
		return 0, false
	}
	return parseCronNumber(field, 0, 9999)
}

// validateCronDayMonthFeasible rejects the common hand-typed impossible date
// (e.g. "31 2" → Feb 31, "31 4" → Apr 31) when both day-of-month and month are
// fixed singletons. February allows day 29 so leap-day schedules stay valid.
// Anything more complex (lists/ranges/steps/specifiers) defers to the search.
func validateCronDayMonthFeasible(domField, monthField string) error {
	day, ok := singleCronNumber(domField)
	if !ok {
		return nil
	}
	month, ok := singleCronNumber(monthField)
	if !ok || month < 1 || month > 12 {
		return nil
	}
	maxDays := [12]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if day > maxDays[month-1] {
		return fmt.Errorf("impossible date in cron: day %d does not occur in month %d", day, month)
	}
	return nil
}

func validateCronPart(part string, min, max int, fieldName string) error {
	base := part
	if slash := strings.Index(part, "/"); slash >= 0 {
		base = part[:slash]
		if base == "" {
			base = "*"
		}
		if _, ok := parseCronNumber(part[slash+1:], 1, max-min+1); !ok {
			return fmt.Errorf("invalid step in cron %s field %q: step must be a positive number", fieldName, part)
		}
	}
	if base == "*" || base == "?" {
		return nil
	}
	if fieldName == "day-of-month" {
		if handled, err := validateDomSpecialToken(base, part, fieldName); handled {
			return err
		}
	}
	if fieldName == "day-of-week" {
		if handled, err := validateDowSpecialToken(base, part, fieldName); handled {
			return err
		}
	}
	if strings.ContainsAny(base, "LW#lw") {
		return fmt.Errorf("unsupported cron token in %s field %q: L/W/# are only valid in the day-of-month (L, LW, L-3, 15W) and day-of-week (5L, 5#3) fields", fieldName, part)
	}
	if dash := strings.Index(base, "-"); dash >= 0 {
		start, ok := parseCronNumber(base[:dash], min, max)
		if !ok {
			return fmt.Errorf("invalid range start in cron %s field %q (allowed %d-%d)", fieldName, part, min, max)
		}
		end, ok := parseCronNumber(base[dash+1:], min, max)
		if !ok {
			return fmt.Errorf("invalid range end in cron %s field %q (allowed %d-%d)", fieldName, part, min, max)
		}
		if end < start {
			return fmt.Errorf("range start %d is after end %d in cron %s field %q", start, end, fieldName, part)
		}
		return nil
	}
	if _, ok := parseCronNumber(base, min, max); !ok {
		return fmt.Errorf("invalid value in cron %s field %q (allowed %d-%d, or *, ?, ranges, steps, lists)", fieldName, part, min, max)
	}
	return nil
}

// validateDomSpecialToken validates a day-of-month special token (L, LW, L-n,
// dW). It returns handled=false for non-special tokens so normal numeric
// validation proceeds.
func validateDomSpecialToken(base, part, fieldName string) (bool, error) {
	upper := strings.ToUpper(base)
	switch {
	case upper == "L" || upper == "LW":
		return true, nil
	case strings.HasPrefix(upper, "L-"):
		if _, ok := parseCronNumber(upper[2:], 1, 30); !ok {
			return true, fmt.Errorf("invalid offset in cron %s field %q: use L-1 .. L-30", fieldName, part)
		}
		return true, nil
	case strings.HasSuffix(upper, "W"):
		if _, ok := parseCronNumber(upper[:len(upper)-1], 1, 31); !ok {
			return true, fmt.Errorf("invalid nearest-weekday day in cron %s field %q: use 1W .. 31W", fieldName, part)
		}
		return true, nil
	}
	return false, nil
}

// validateDowSpecialToken validates a day-of-week special token (dL last weekday,
// d#k nth weekday). Weekday accepts 0..7 (0 and 7 both Sunday).
func validateDowSpecialToken(base, part, fieldName string) (bool, error) {
	upper := strings.ToUpper(base)
	if upper == "L" {
		return true, nil
	}
	if hash := strings.Index(upper, "#"); hash >= 0 {
		if _, ok := parseCronNumber(upper[:hash], 0, 7); !ok {
			return true, fmt.Errorf("invalid weekday in cron %s field %q: use 0#n .. 7#n (0 or 7 = Sunday)", fieldName, part)
		}
		if _, ok := parseCronNumber(upper[hash+1:], 1, 5); !ok {
			return true, fmt.Errorf("invalid occurrence in cron %s field %q: use d#1 .. d#5", fieldName, part)
		}
		return true, nil
	}
	if strings.HasSuffix(upper, "L") {
		if _, ok := parseCronNumber(upper[:len(upper)-1], 0, 7); !ok {
			return true, fmt.Errorf("invalid weekday in cron %s field %q: use 0L .. 7L (last weekday of month)", fieldName, part)
		}
		return true, nil
	}
	return false, nil
}

func cronFieldMatches(expr string, value int, min int, max int) bool {
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if cronPartMatches(part, value, min, max) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value int, min int, max int) bool {
	step := 1
	base := part
	if slash := strings.Index(part, "/"); slash >= 0 {
		base = part[:slash]
		if base == "" {
			base = "*"
		}
		parsedStep, ok := parseCronNumber(part[slash+1:], 1, max-min+1)
		if !ok {
			return false
		}
		step = parsedStep
	}
	if base == "?" {
		// "?" ("no specific value", used in day-of-month/day-of-week) is treated
		// as a wildcard, matching the standard Quartz convention.
		base = "*"
	}
	if base == "*" {
		return (value-min)%step == 0
	}
	if dash := strings.Index(base, "-"); dash >= 0 {
		start, ok := parseCronNumber(base[:dash], min, max)
		if !ok {
			return false
		}
		end, ok := parseCronNumber(base[dash+1:], min, max)
		if !ok || end < start {
			return false
		}
		if value < start || value > end {
			return false
		}
		return (value-start)%step == 0
	}
	number, ok := parseCronNumber(base, min, max)
	if !ok {
		return false
	}
	return value == number
}

func parseCronNumber(raw string, min int, max int) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		value = value*10 + int(ch-'0')
	}
	if value < min || value > max {
		return 0, false
	}
	return value, true
}

// scheduleEvalLocation resolves the timezone in which a schedule's cron fields
// are evaluated. An empty or unparseable timezone falls back to UTC so a
// mis-typed zone degrades to the previous (UTC) behavior rather than failing the
// schedule. next_run_at is always stored in UTC regardless of this location.
func scheduleEvalLocation(timezone string) *time.Location {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return time.UTC
	}
	location, err := cachedScheduleLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

var scheduleLocationCache sync.Map // map[string]*time.Location

func cachedScheduleLocation(timezone string) (*time.Location, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || timezone == "UTC" {
		return time.UTC, nil
	}
	if cached, ok := scheduleLocationCache.Load(timezone); ok {
		return cached.(*time.Location), nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	actual, _ := scheduleLocationCache.LoadOrStore(timezone, location)
	return actual.(*time.Location), nil
}

// cronMatchesDayOfMonthAndWeek applies POSIX cron semantics for the
// day-of-month (field 3) and day-of-week (field 5) fields: when BOTH are
// restricted (neither is "*" nor "?"), a candidate matches if EITHER field
// matches; otherwise both must match (and an unrestricted field always matches,
// so this reduces to the single restricted field). This fixes the prior AND-only
// behavior where "0 9 15 * 1" only fired on a Monday that was also the 15th.
// Both fields additionally honor the Quartz day specifiers (L/LW/L-n/dW in
// day-of-month; dL/d#k in day-of-week).
func cronMatchesDayOfMonthAndWeek(domField, dowField string, t time.Time) bool {
	domField = strings.TrimSpace(domField)
	dowField = strings.TrimSpace(dowField)
	domRestricted := domField != "*" && domField != "?" && domField != ""
	dowRestricted := dowField != "*" && dowField != "?" && dowField != ""
	domMatch := cronDomMatches(domField, t)
	dowMatch := cronDowMatches(dowField, t)
	if domRestricted && dowRestricted {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

func cronDomMatches(field string, t time.Time) bool {
	field = strings.TrimSpace(field)
	if field == "*" || field == "?" || field == "" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronDomPartMatches(strings.TrimSpace(part), t) {
			return true
		}
	}
	return false
}

func cronDomPartMatches(part string, t time.Time) bool {
	if part == "" {
		return false
	}
	upper := strings.ToUpper(part)
	last := daysInMonth(t.Year(), t.Month(), t.Location())
	switch {
	case upper == "L":
		return t.Day() == last
	case upper == "LW":
		return t.Day() == nearestWeekday(t.Year(), t.Month(), last, t.Location())
	case strings.HasPrefix(upper, "L-"):
		n, ok := parseCronNumber(upper[2:], 1, 30)
		if !ok {
			return false
		}
		return t.Day() == last-n
	case strings.HasSuffix(upper, "W"):
		d, ok := parseCronNumber(upper[:len(upper)-1], 1, 31)
		if !ok {
			return false
		}
		return t.Day() == nearestWeekday(t.Year(), t.Month(), d, t.Location())
	default:
		return cronPartMatches(part, t.Day(), 1, 31)
	}
}

func cronDowMatches(field string, t time.Time) bool {
	field = strings.TrimSpace(field)
	if field == "*" || field == "?" || field == "" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronDowPartMatches(strings.TrimSpace(part), t) {
			return true
		}
	}
	return false
}

func cronDowPartMatches(part string, t time.Time) bool {
	if part == "" {
		return false
	}
	weekday := int(t.Weekday()) // 0=Sunday .. 6=Saturday
	upper := strings.ToUpper(part)
	if upper == "L" {
		return weekday == int(time.Saturday) && t.Day()+7 > daysInMonth(t.Year(), t.Month(), t.Location())
	}
	if hash := strings.Index(upper, "#"); hash >= 0 {
		d, ok := parseCronNumber(upper[:hash], 0, 7)
		if !ok {
			return false
		}
		k, ok := parseCronNumber(upper[hash+1:], 1, 5)
		if !ok {
			return false
		}
		if normalizeCronDow(d) != weekday {
			return false
		}
		return (t.Day()-1)/7+1 == k
	}
	if strings.HasSuffix(upper, "L") {
		d, ok := parseCronNumber(upper[:len(upper)-1], 0, 7)
		if !ok {
			return false
		}
		if normalizeCronDow(d) != weekday {
			return false
		}
		return t.Day()+7 > daysInMonth(t.Year(), t.Month(), t.Location())
	}
	if cronPartMatches(part, weekday, 0, 7) {
		return true
	}
	// Sunday (weekday 0) also matches a literal 7, e.g. "7" or a range like "5-7".
	return weekday == 0 && cronPartMatches(part, 7, 0, 7)
}

func normalizeCronDow(d int) int {
	if d == 7 {
		return 0
	}
	return d
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	firstOfNext := time.Date(year, month, 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	return firstOfNext.AddDate(0, 0, -1).Day()
}

// nearestWeekday returns the day-of-month of the weekday (Mon-Fri) nearest to
// `day`, never crossing the month boundary (Quartz "W" semantics: a Saturday
// shifts to the prior Friday unless that leaves the month, in which case it
// shifts to the following Monday, and vice versa for Sunday).
func nearestWeekday(year int, month time.Month, day int, loc *time.Location) int {
	last := daysInMonth(year, month, loc)
	if day > last {
		day = last
	}
	if day < 1 {
		day = 1
	}
	switch time.Date(year, month, day, 0, 0, 0, 0, loc).Weekday() {
	case time.Saturday:
		if day-1 >= 1 {
			return day - 1
		}
		return day + 2
	case time.Sunday:
		if day+1 <= last {
			return day + 1
		}
		return day - 2
	default:
		return day
	}
}

func scheduleAllowsCandidate(schedule *storage.ScenarioSchedule, candidate time.Time) (bool, error) {
	if scheduleDisabledAt(schedule, candidate) {
		return false, nil
	}
	for _, window := range schedule.BlackoutWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return false, nil
		}
	}
	for _, window := range schedule.MaintenanceWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return false, nil
		}
	}
	if len(schedule.AvailabilityWindows) == 0 {
		return true, nil
	}
	for _, window := range schedule.AvailabilityWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

// normalizeSchedulePauseBound drops zero-value pause bounds to nil and stores the
// rest in UTC so date-range comparisons in scheduleDisabledAt are unambiguous.
func normalizeSchedulePauseBound(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// scheduleDisabledAt reports whether candidate falls inside the schedule's
// optional date-range pause [DisabledFrom, DisabledUntil). Either bound may be
// nil for an open-ended pause; with both nil the schedule is never paused.
func scheduleDisabledAt(schedule *storage.ScenarioSchedule, candidate time.Time) bool {
	if schedule == nil {
		return false
	}
	return dateRangePauseActive(schedule.DisabledFrom, schedule.DisabledUntil, candidate)
}

// dateRangePauseActive reports whether candidate falls inside the optional pause
// window [disabledFrom, disabledUntil) — either bound may be nil for an
// open-ended pause; both nil means never paused. Shared by scenario and
// execution-plan schedules.
func dateRangePauseActive(disabledFrom, disabledUntil *time.Time, candidate time.Time) bool {
	if disabledFrom == nil && disabledUntil == nil {
		return false
	}
	if disabledFrom != nil && candidate.Before(*disabledFrom) {
		return false
	}
	if disabledUntil != nil && !candidate.Before(*disabledUntil) {
		return false
	}
	return true
}

func scheduleWindowContains(window storage.ScenarioScheduleWindow, fallbackTimezone string, candidate time.Time) (bool, error) {
	timezone := firstNonEmpty(strings.TrimSpace(window.Timezone), strings.TrimSpace(fallbackTimezone))
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := cachedScheduleLocation(timezone)
	if err != nil {
		return false, fmt.Errorf("invalid schedule window timezone %q: %w", timezone, err)
	}
	local := candidate.In(location)
	if len(window.DaysOfWeek) > 0 && !scheduleWindowDayMatches(window.DaysOfWeek, int(local.Weekday())) {
		return false, nil
	}
	startMinute, err := parseScheduleWindowMinute(window.StartTime)
	if err != nil {
		return false, err
	}
	endMinute, err := parseScheduleWindowMinute(window.EndTime)
	if err != nil {
		return false, err
	}
	minuteOfDay := local.Hour()*60 + local.Minute()
	if startMinute == endMinute {
		return true, nil
	}
	if startMinute < endMinute {
		return minuteOfDay >= startMinute && minuteOfDay < endMinute, nil
	}
	return minuteOfDay >= startMinute || minuteOfDay < endMinute, nil
}

func scheduleWindowDayMatches(days []int, weekday int) bool {
	for _, day := range days {
		if day == weekday {
			return true
		}
	}
	return false
}

func parseScheduleWindowMinute(raw string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid schedule window time %q: expected HH:MM", raw)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func (tr *TestRunner) listAllScenarioSchedulesLocked() []*storage.ScenarioSchedule {
	out := make([]*storage.ScenarioSchedule, 0)
	for _, scenario := range tr.scenarios {
		for _, schedule := range scenario.Schedules {
			out = append(out, cloneScenarioSchedule(schedule))
		}
	}
	return out
}

func (tr *TestRunner) runScenarioScheduleLocked(schedule *storage.ScenarioSchedule, now time.Time) (*ManagedTest, error) {
	if schedule == nil {
		return nil, fmt.Errorf("schedule is required")
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	plannedAt := now
	if schedule.NextRunAt != nil {
		plannedAt = *schedule.NextRunAt
	}
	handled, resumeOccurrence, recoveredPrepared, err := tr.reconcileScenarioScheduleOccurrenceLocked(schedule, plannedAt, now)
	if err != nil {
		return nil, err
	} else if handled {
		return recoveredPrepared, nil
	}
	if allowed, err := scheduleAllowsCandidate(schedule, plannedAt); err != nil {
		return nil, err
	} else if !allowed {
		schedule.ConflictSummary = "Environment availability conflict: planned occurrence falls outside allowed schedule windows."
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "environment_unavailable", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	schedule.ConflictSummary = tr.scenarioScheduleConflictSummaryLocked(schedule, scenario)
	if strings.TrimSpace(schedule.ConflictSummary) != "" {
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "schedule_conflict", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if schedule.NextRequiredAction == "approval_required" && schedule.PendingApprovalID != "" {
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "approval_required", "Schedule requires approval before materialization.", "", "schedule requires approval", now, func(occurrence *storage.ScenarioScheduleOccurrence) {
			occurrence.ApprovalID = schedule.PendingApprovalID
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	occurrence := resumeOccurrence
	if occurrence == nil {
		occurrence = tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "materializing", "due", "", nil, now)
	}
	occurrence.Status = "materializing"
	occurrence.ReasonCode = "due"
	occurrence.ConflictSummary = ""
	occurrence.MaterializedAt = nil
	occurrence.UpdatedAt = now
	if strings.TrimSpace(occurrence.TestID) == "" {
		occurrence.TestID = uuid.New().String()
	}
	if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
		return nil, err
	}
	runName := strings.TrimSpace(schedule.RunNameTemplate)
	if runName == "" {
		runName = fmt.Sprintf("Scheduled Run %s", now.Format(time.RFC3339))
	}
	test, err := tr.buildManagedTestLocked(CreateTestRequest{
		Name:       runName,
		ScenarioID: schedule.ScenarioID,
		ReleaseID:  schedule.ReleaseID,
	}, occurrence.TestID, "QUEUED")
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "create_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence); saveErr != nil {
			slog.Warn("failed to persist scenario occurrence creation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		return nil, err
	}
	// Recurring runs use the same launch-window evaluator as manual/scheduled
	// starts and preflight. A run that would launch into a capacity/concurrency/
	// reservation-window conflict is recorded as a blocked occurrence (surfaced on
	// the run calendar) instead of being launched anyway. The test is built but
	// not yet registered, so it does not self-conflict; the "materializing"
	// occurrence already dedups this schedule's own projection.
	if windowErr := tr.launchStartWindowErrorLocked(test, now, test.ID, schedule.ID); windowErr != nil {
		schedule.ConflictSummary = windowErr.Error()
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "schedule_conflict", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	// Bind the schedule's default capacity timeslot, if configured, to this
	// occurrence's run (same atomic bind ad-hoc runs use). Lenient: if the slot is
	// missing, unapproved, already bound, or doesn't fit, block this occurrence
	// with a clear reason rather than failing the whole schedule — schedules are
	// long-lived and the slot may be transiently unavailable.
	if strings.TrimSpace(schedule.TimeslotID) != "" {
		if bindErr := tr.validateAndBindTimeslotLocked(strings.TrimSpace(schedule.TimeslotID), test); bindErr != nil {
			schedule.ConflictSummary = bindErr.Error()
			if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "timeslot_unavailable", bindErr.Error(), "resolve_conflict", bindErr.Error(), now, nil); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	tr.tests[test.ID] = test
	tr.indexRefKeyLocked(test.RefKey, test.ID)
	if err := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) }); err != nil {
		delete(tr.tests, test.ID)
		tr.removeRefKeyLocked(test.RefKey)
		if tr.reservationStore != nil && test.TimeslotID != nil {
			if _, clearErr := tr.reservationStore.ClearCapacityReservationBinding(*test.TimeslotID, test.ID, time.Now().UTC()); clearErr != nil {
				slog.Warn("failed to release scheduled test timeslot after persistence failure", "test_id", test.ID, "timeslot_id", *test.TimeslotID, "error", clearErr)
				tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
			}
		}
		occurrence.Status = "failed"
		occurrence.ReasonCode = "create_persist_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence); saveErr != nil {
			slog.Warn("failed to persist scheduled test creation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		return nil, fmt.Errorf("persist scheduled test: %w", err)
	}
	tr.broadcastEvent("test_created", test)
	// The launch window was just evaluated above; skip the redundant re-check.
	prepared, err := tr.prepareTestStartLocked(test.ID, StartTestOptions{SkipLaunchWindowPreflight: true})
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "start_failed"
		occurrence.TestID = test.ID
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence); saveErr != nil {
			slog.Warn("failed to persist scheduled test start failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "prepare scheduled test: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	occurrence.Status = "materialized"
	occurrence.ReasonCode = "started"
	occurrence.TestID = test.ID
	occurrence.MaterializedAt = &now
	occurrence.UpdatedAt = now
	if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "save materialized schedule occurrence: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	schedule.LastRunTestID = test.ID
	schedule.LastRunAt = &now
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	schedule.NextRequiredAction = ""
	schedule.LastError = ""
	advanceFrom := now
	if normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all" && plannedAt.Before(now) {
		advanceFrom = plannedAt
	}
	next, err := nextScenarioScheduleRun(schedule, advanceFrom)
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "schedule_calculation_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence); saveErr != nil {
			slog.Warn("failed to persist schedule calculation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "calculate next scheduled run: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	schedule.NextRunAt = next
	schedule.UpdatedAt = now
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "schedule_persist_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence); saveErr != nil {
			slog.Warn("failed to persist schedule persistence failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "save updated schedule: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	return prepared, nil
}

func (tr *TestRunner) failScheduledTestBeforeLaunchLocked(test *ManagedTest, now time.Time, message string) error {
	if test == nil {
		return nil
	}
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousEndedAt := cloneTimePtr(test.EndedAt)
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = message
	test.EndedAt = cloneTimePtr(&now)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		test.EndedAt = previousEndedAt
		retryErr := fmt.Errorf("persist scheduled test failure: %w", err)
		slog.Error("failed to persist scheduled test failure; cleanup and notification deferred", "test_id", test.ID, "error", retryErr)
		tr.enqueueScheduledTestFailureRetry(test.ID, now, message)
		return retryErr
	}
	if test.cancel != nil {
		test.cancel()
	}
	var releaseErr error
	if tr.reservationStore != nil && test.TimeslotID != nil {
		if _, err := tr.reservationStore.ClearCapacityReservationBinding(*test.TimeslotID, test.ID, now.UTC()); err != nil {
			slog.Warn("failed to release failed scheduled test timeslot", "test_id", test.ID, "timeslot_id", *test.TimeslotID, "error", err)
			releaseErr = fmt.Errorf("release failed scheduled test timeslot: %w", err)
			tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
		}
	}
	tr.broadcastEvent("test_failed", test)
	return releaseErr
}

func (tr *TestRunner) enqueueScheduledTestFailureRetry(testID string, now time.Time, message string) {
	tr.enqueueTerminalRetry(testID, "FAILED scheduled pre-launch", func() error {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		test := tr.tests[testID]
		if test == nil {
			return nil
		}
		if test.Status == "FAILED" || test.Status == "COMPLETE" || test.Status == "ABORTED" {
			return nil
		}
		return tr.failScheduledTestBeforeLaunchLocked(test, now, message)
	})
}

func (tr *TestRunner) reconcileScenarioScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule, plannedAt time.Time, now time.Time) (bool, *storage.ScenarioScheduleOccurrence, *ManagedTest, error) {
	if tr == nil || tr.scenarioStore == nil || schedule == nil {
		return false, nil, nil, nil
	}
	existing, err := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt.UTC())
	if err != nil || existing == nil {
		return false, nil, nil, err
	}
	var recoveredPrepared *ManagedTest
	status := strings.ToLower(strings.TrimSpace(existing.Status))
	if status == "" || status == "materializing" {
		// A correlated durable test means the prior controller crossed the test
		// persistence boundary before crashing. Finalize this occurrence and
		// advance the schedule instead of launching a duplicate. If no test row
		// exists, resume materialization with the preassigned ID; this safely covers
		// crashes between occurrence intent and test persistence.
		if strings.TrimSpace(existing.TestID) == "" {
			existing.TestID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:scenario-schedule-occurrence:"+existing.ID)).String()
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceLocked(existing); err != nil {
				return false, nil, nil, err
			}
		}
		if recovered := tr.tests[existing.TestID]; recovered == nil {
			return false, cloneScenarioScheduleOccurrence(existing), nil, nil
		} else if recovered.Status == "QUEUED" {
			// The prior controller persisted the test but crashed before preparing
			// its execution context. Resume that exact row and return it to the
			// scheduler's normal post-lock launch path.
			prepared, err := tr.prepareTestStartLocked(recovered.ID, StartTestOptions{SkipLaunchWindowPreflight: true})
			if err != nil {
				existing.Status = "failed"
				existing.ReasonCode = "start_failed"
				existing.ConflictSummary = err.Error()
				existing.UpdatedAt = now
				saveErr := tr.saveScenarioScheduleOccurrenceLocked(existing)
				failErr := tr.failScheduledTestBeforeLaunchLocked(recovered, now, "resume correlated scheduled test: "+err.Error())
				return true, nil, nil, errors.Join(fmt.Errorf("resume correlated scheduled test %s: %w", recovered.ID, err), saveErr, failErr)
			}
			recoveredPrepared = prepared
		} else if recovered.Status == "INITIALIZING" {
			// There is no safe commit point from which an INITIALIZING test can be
			// resumed: after restart its execution context is gone, while in this
			// process it may be the residue of a failed occurrence/schedule write.
			// Fail it closed instead of converting the occurrence to materialized
			// and either stranding or launching an ambiguously prepared run.
			cause := fmt.Errorf("correlated scheduled test %s was left initializing before schedule recovery committed", recovered.ID)
			return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recovered, now, "recovery_initializing_test", cause.Error(), cause)
		} else if recovered.Status == "FAILED" || recovered.Status == "ABORTED" {
			// A prior recovery attempt may have durably failed the test but lost the
			// occurrence update. Repair that half-written state idempotently.
			existing.Status = "failed"
			existing.ReasonCode = "recovered_test_failed"
			existing.ConflictSummary = firstNonEmpty(recovered.Error, "correlated scheduled test is terminal")
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceLocked(existing); err != nil {
				return true, nil, nil, err
			}
			status = "failed"
		}
		if status != "failed" {
			existing.Status = "materialized"
			existing.ReasonCode = "recovered"
			if existing.MaterializedAt == nil {
				materializedAt := now
				existing.MaterializedAt = &materializedAt
			}
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceLocked(existing); err != nil {
				if recoveredPrepared != nil {
					return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_occurrence_persist_failed", "save recovered materialized schedule occurrence: "+err.Error(), err)
				}
				return true, nil, nil, err
			}
			status = "materialized"
		}
	}
	if existing.ScheduleRevision != schedule.Revision && (status == "blocked" || status == "failed") {
		return false, nil, nil, nil
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(existing)
	schedule.UpdatedAt = now
	switch status {
	case "materialized", "queued", "completed", "approved":
		if existing.TestID != "" {
			schedule.LastRunTestID = existing.TestID
		}
		if existing.MaterializedAt != nil {
			materializedAt := *existing.MaterializedAt
			schedule.LastRunAt = &materializedAt
		}
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			if recoveredPrepared != nil {
				return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_schedule_calculation_failed", "calculate next run for recovered schedule: "+err.Error(), err)
			}
			return true, nil, nil, err
		}
		schedule.NextRunAt = next
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	case "skipped":
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, nil, nil, err
		}
		schedule.NextRunAt = next
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	case "blocked":
		schedule.LastError = firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
		switch existing.ReasonCode {
		case "approval_required":
			schedule.NextRequiredAction = "approval_required"
			schedule.PendingApprovalID = existing.ApprovalID
		case "schedule_conflict":
			schedule.NextRequiredAction = "resolve_conflict"
		case "missed_run_requires_decision":
			schedule.NextRequiredAction = "operator_decision_required"
		}
	case "failed":
		schedule.LastError = firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
		schedule.NextRequiredAction = "operator_decision_required"
	default:
		return false, nil, nil, nil
	}
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		if recoveredPrepared != nil {
			return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_schedule_persist_failed", "save recovered schedule: "+err.Error(), err)
		}
		return true, nil, nil, err
	}
	return true, nil, recoveredPrepared, nil
}

// failRecoveredScenarioSchedulePreparationLocked closes every durable boundary
// crossed while recovering a materializing occurrence. It makes the occurrence
// and schedule operator-visible before failing the prepared test; each write is
// independently retryable by a later sweep, so a partial failure converges on
// the same terminal state without ever returning the test for launch.
func (tr *TestRunner) failRecoveredScenarioSchedulePreparationLocked(schedule *storage.ScenarioSchedule, occurrence *storage.ScenarioScheduleOccurrence, test *ManagedTest, now time.Time, reasonCode, message string, cause error) error {
	if occurrence == nil {
		return errors.Join(cause, tr.failScheduledTestBeforeLaunchLocked(test, now, message))
	}
	occurrence.Status = "failed"
	occurrence.ReasonCode = reasonCode
	occurrence.ConflictSummary = message
	occurrence.UpdatedAt = now
	occurrenceErr := tr.saveScenarioScheduleOccurrenceLocked(occurrence)

	var scheduleErr error
	if schedule != nil {
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.LastError = message
		schedule.NextRequiredAction = "operator_decision_required"
		schedule.UpdatedAt = now
		scheduleErr = tr.saveScenarioScheduleLocked(schedule)
	}
	testErr := tr.failScheduledTestBeforeLaunchLocked(test, now, message)
	return errors.Join(cause, occurrenceErr, scheduleErr, testErr)
}

// blockScheduleOccurrenceLocked records a "blocked" occurrence for schedule at
// plannedAt, persists it, then updates the schedule's LastOccurrence / LastError /
// UpdatedAt (and NextRequiredAction when non-empty). The optional mutate hook lets
// callers set extra occurrence fields (e.g. ApprovalID) before persisting. It
// returns the first persistence error encountered.
func (tr *TestRunner) blockScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule, plannedAt time.Time, reasonCode, summary, nextRequiredAction, lastError string, now time.Time, mutate func(*storage.ScenarioScheduleOccurrence)) error {
	occurrence := tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "blocked", reasonCode, summary, nil, now)
	if mutate != nil {
		mutate(occurrence)
	}
	if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
		return err
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	if nextRequiredAction != "" {
		schedule.NextRequiredAction = nextRequiredAction
	}
	schedule.LastError = lastError
	schedule.UpdatedAt = now
	return tr.saveScenarioScheduleLocked(schedule)
}

func (tr *TestRunner) buildScenarioScheduleOccurrence(schedule *storage.ScenarioSchedule, plannedAt time.Time, status string, reasonCode string, conflictSummary string, materializedAt *time.Time, now time.Time) *storage.ScenarioScheduleOccurrence {
	return &storage.ScenarioScheduleOccurrence{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        normalizeProjectID(schedule.ProjectID),
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt.UTC(),
		MaterializedAt:   materializedAt,
		Status:           status,
		ReasonCode:       reasonCode,
		TestID:           "",
		ConflictSummary:  conflictSummary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func cloneScenarioSchedule(schedule *storage.ScenarioSchedule) *storage.ScenarioSchedule {
	if schedule == nil {
		return nil
	}
	cloned := *schedule
	if schedule.LastRunAt != nil {
		lastRun := *schedule.LastRunAt
		cloned.LastRunAt = &lastRun
	}
	if schedule.NextRunAt != nil {
		nextRun := *schedule.NextRunAt
		cloned.NextRunAt = &nextRun
	}
	if schedule.DisabledFrom != nil {
		disabledFrom := *schedule.DisabledFrom
		cloned.DisabledFrom = &disabledFrom
	}
	if schedule.DisabledUntil != nil {
		disabledUntil := *schedule.DisabledUntil
		cloned.DisabledUntil = &disabledUntil
	}
	cloned.BlackoutWindows = cloneScenarioScheduleWindows(schedule.BlackoutWindows)
	cloned.MaintenanceWindows = cloneScenarioScheduleWindows(schedule.MaintenanceWindows)
	cloned.AvailabilityWindows = cloneScenarioScheduleWindows(schedule.AvailabilityWindows)
	if schedule.Governance != nil {
		governance := *schedule.Governance
		governance.RBACReview.RequiredRoles = append([]string(nil), schedule.Governance.RBACReview.RequiredRoles...)
		governance.QuotaReview.Findings = append([]string(nil), schedule.Governance.QuotaReview.Findings...)
		cloned.Governance = &governance
	}
	cloned.LastOccurrence = cloneScenarioScheduleOccurrence(schedule.LastOccurrence)
	return &cloned
}

func cloneScenarioScheduleWindows(windows []storage.ScenarioScheduleWindow) []storage.ScenarioScheduleWindow {
	if len(windows) == 0 {
		return nil
	}
	cloned := append([]storage.ScenarioScheduleWindow(nil), windows...)
	for index := range cloned {
		cloned[index].DaysOfWeek = append([]int(nil), windows[index].DaysOfWeek...)
	}
	return cloned
}

func cloneScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) *storage.ScenarioScheduleOccurrence {
	if occurrence == nil {
		return nil
	}
	cloned := *occurrence
	if occurrence.MaterializedAt != nil {
		materializedAt := *occurrence.MaterializedAt
		cloned.MaterializedAt = &materializedAt
	}
	return &cloned
}

func (tr *TestRunner) CreateScenarioSchedule(scenarioID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.upsertScenarioScheduleLocked(scenarioID, uuid.New().String(), nil, req)
}

// ScenarioSchedulePreview is the result of a (non-persisting) schedule preview:
// the next N UTC fire times for a candidate cron/timezone/window configuration,
// plus a pre-save conflict summary (empty when the schedule would materialize
// cleanly) computed with the same evaluator the materialization sweep uses, so
// operators can fix a quota/capacity/release conflict before saving instead of
// discovering blocked occurrences on the calendar afterward.
type ScenarioSchedulePreview struct {
	CronExpr        string      `json:"cron_expr"`
	Timezone        string      `json:"timezone"`
	Occurrences     []time.Time `json:"occurrences"`
	Count           int         `json:"count"`
	ConflictSummary string      `json:"conflict_summary,omitempty"`
}

// PreviewScenarioScheduleRuns computes the next `count` fire times for a candidate
// schedule WITHOUT persisting anything. It reuses the production evaluator
// (nextScenarioScheduleRun + scheduleAllowsCandidate), so the preview honors the
// declared timezone, DST, POSIX day-of-month/day-of-week OR semantics, the
// Quartz day specifiers, blackout/maintenance/availability windows, and the
// date-range pause exactly as the scheduler will at runtime — the preview cannot
// drift from real behavior.
func (tr *TestRunner) PreviewScenarioScheduleRuns(scenarioID string, req CreateScenarioScheduleRequest, count int) (*ScenarioSchedulePreview, error) {
	if _, ok := tr.GetScenario(scenarioID); !ok {
		return nil, fmt.Errorf("scenario not found: %s", scenarioID)
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if count <= 0 {
		count = 5
	}
	if count > 50 {
		count = 50
	}
	disabledFrom := normalizeSchedulePauseBound(req.DisabledFrom)
	disabledUntil := normalizeSchedulePauseBound(req.DisabledUntil)
	if disabledFrom != nil && disabledUntil != nil && !disabledFrom.Before(*disabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	schedule := &storage.ScenarioSchedule{
		ScenarioID:          scenarioID,
		Name:                strings.TrimSpace(req.Name),
		ReleaseID:           req.ReleaseID,
		CronExpr:            cronExpr,
		Timezone:            tz,
		BlackoutWindows:     req.BlackoutWindows,
		MaintenanceWindows:  req.MaintenanceWindows,
		AvailabilityWindows: req.AvailabilityWindows,
		DisabledFrom:        disabledFrom,
		DisabledUntil:       disabledUntil,
	}
	occurrences := make([]time.Time, 0, count)
	cursor := time.Now().UTC()
	for i := 0; i < count; i++ {
		next, err := nextScenarioScheduleRun(schedule, cursor)
		if err != nil || next == nil {
			break
		}
		occurrences = append(occurrences, *next)
		cursor = next.Add(time.Minute)
	}
	// Pre-save conflict check: surface the same quota/capacity/release conflict the
	// materialization sweep would record, so it can be fixed before saving. Uses
	// the production evaluator under a read lock (it builds a non-registered preview
	// test, exactly like the estimate/preflight paths).
	conflictSummary := ""
	func() {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		scenario, ok := tr.scenarios[tr.resolveRefKeyLocked(scenarioID)]
		if !ok {
			return
		}
		conflictSummary = tr.previewScheduleConflictSummaryLocked(schedule, scenario)
	}()
	return &ScenarioSchedulePreview{CronExpr: cronExpr, Timezone: tz, Occurrences: occurrences, Count: len(occurrences), ConflictSummary: conflictSummary}, nil
}

func (tr *TestRunner) UpdateScenarioSchedule(scenarioID string, scheduleID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false, nil
	}
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			updated, err := tr.upsertScenarioScheduleLocked(scenarioID, scheduleID, schedule, req)
			return updated, true, err
		}
	}
	return nil, false, nil
}

func (tr *TestRunner) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false
	}
	out := make([]*storage.ScenarioSchedule, 0, len(scenario.Schedules))
	for _, schedule := range scenario.Schedules {
		cloned := cloneScenarioSchedule(schedule)
		tr.attachLatestScenarioScheduleOccurrenceLocked(cloned)
		out = append(out, cloned)
	}
	return out, true
}

func (tr *TestRunner) attachLatestScenarioScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule) {
	if schedule == nil || tr.scenarioStore == nil {
		return
	}
	if store, ok := tr.scenarioStore.(latestScenarioScheduleOccurrenceStore); ok {
		occurrence, err := store.LatestScenarioScheduleOccurrence(schedule.ID)
		if err == nil && occurrence != nil {
			schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		}
		return
	}
	occurrences, err := tr.scenarioStore.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil || len(occurrences) == 0 {
		return
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrences[0])
}

func (tr *TestRunner) DeleteScenarioSchedule(scenarioID string, scheduleID string) (bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return false, nil
	}
	filtered := scenario.Schedules[:0]
	deleted := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			deleted = true
			continue
		}
		filtered = append(filtered, schedule)
	}
	if !deleted {
		return false, nil
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.DeleteScenarioSchedule(scheduleID); err != nil {
			return false, err
		}
	}
	scenario.Schedules = append([]*storage.ScenarioSchedule(nil), filtered...)
	return true, nil
}

func (tr *TestRunner) upsertScenarioScheduleLocked(scenarioID string, scheduleID string, existing *storage.ScenarioSchedule, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", scenarioID)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("schedule name is required")
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if cronExpr == "" {
		return nil, fmt.Errorf("schedule cron_expr is required")
	}
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	schedule := &storage.ScenarioSchedule{
		ID:              scheduleID,
		ScenarioID:      scenarioID,
		ProjectID:       normalizeProjectID(scenario.ProjectID),
		Revision:        1,
		Name:            name,
		CronExpr:        cronExpr,
		Timezone:        strings.TrimSpace(req.Timezone),
		ReleaseID:       strings.TrimSpace(req.ReleaseID),
		TimeslotID:      strings.TrimSpace(req.TimeslotID),
		RunNameTemplate: strings.TrimSpace(req.RunNameTemplate),
		Enabled:         enabled,
		ApprovalPolicy:  normalizeScenarioScheduleApprovalPolicy(req.ApprovalPolicy),
		MissedRunPolicy: normalizeScenarioScheduleMissedRunPolicy(req.MissedRunPolicy),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing != nil {
		schedule.Revision = existing.Revision + 1
		schedule.CreatedAt = existing.CreatedAt
		schedule.OwnerSubject = existing.OwnerSubject
		schedule.OwnerDisplayName = existing.OwnerDisplayName
		schedule.LastRunTestID = existing.LastRunTestID
		schedule.LastRunAt = existing.LastRunAt
		schedule.LastError = existing.LastError
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(existing.LastOccurrence)
	}
	schedule.BlackoutWindows = append([]storage.ScenarioScheduleWindow(nil), req.BlackoutWindows...)
	schedule.MaintenanceWindows = append([]storage.ScenarioScheduleWindow(nil), req.MaintenanceWindows...)
	schedule.AvailabilityWindows = append([]storage.ScenarioScheduleWindow(nil), req.AvailabilityWindows...)
	schedule.DisabledFrom = normalizeSchedulePauseBound(req.DisabledFrom)
	schedule.DisabledUntil = normalizeSchedulePauseBound(req.DisabledUntil)
	if schedule.DisabledFrom != nil && schedule.DisabledUntil != nil && !schedule.DisabledFrom.Before(*schedule.DisabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	if strings.TrimSpace(schedule.OwnerSubject) == "" {
		schedule.OwnerSubject = strings.TrimSpace(req.OwnerSubject)
	}
	if strings.TrimSpace(schedule.OwnerDisplayName) == "" {
		schedule.OwnerDisplayName = strings.TrimSpace(req.OwnerDisplayName)
	}
	schedule.Governance = tr.scenarioScheduleGovernanceLocked(scenario, schedule, req, now)
	if schedule.Timezone == "" {
		schedule.Timezone = "UTC"
	}
	if schedule.RunNameTemplate == "" {
		schedule.RunNameTemplate = scenario.Name
	}
	schedule.ConflictSummary = tr.scenarioScheduleConflictSummaryLocked(schedule, scenario)
	schedule.NextRequiredAction = scenarioScheduleNextRequiredAction(schedule)
	var approvalToSave *storage.ScenarioScheduleApproval
	if schedule.NextRequiredAction == "approval_required" {
		approvalToSave = tr.buildScenarioScheduleApproval(schedule, now, strings.TrimSpace(req.OwnerSubject))
		schedule.PendingApprovalID = approvalToSave.ID
	} else {
		schedule.PendingApprovalID = ""
	}
	// An open-ended pause (disabled_from set, disabled_until nil) makes every future
	// candidate ineligible, so computing a next run would exhaust the full search
	// horizon and return a misleading "impossible day/month" error. Short-circuit to
	// a paused state instead (mirrors the execution-plan schedule path).
	if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
		schedule.NextRunAt = nil
	} else {
		nextRun, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return nil, err
		}
		schedule.NextRunAt = nextRun
		// Preserve the prior next-run only when it exists and no schedule-affecting
		// field changed. A nil existing NextRunAt means the schedule was previously
		// paused/unset, so it must NOT be preserved — otherwise clearing an
		// open-ended pause (which doesn't touch the fields compared below) would
		// leave the resumed schedule stuck at NextRunAt=nil and never fire.
		if existing != nil && existing.NextRunAt != nil && existing.Enabled == schedule.Enabled && existing.CronExpr == schedule.CronExpr && existing.Timezone == schedule.Timezone && existing.ReleaseID == schedule.ReleaseID && existing.RunNameTemplate == schedule.RunNameTemplate && existing.Name == schedule.Name {
			schedule.NextRunAt = existing.NextRunAt
		}
	}
	if approvalToSave != nil {
		if atomicStore, ok := tr.scenarioStore.(atomicScenarioScheduleApprovalWriteStore); ok {
			expectedRevision := 0
			if existing != nil {
				expectedRevision = existing.Revision
			}
			changed, err := atomicStore.SaveScenarioScheduleWithApproval(schedule, approvalToSave, expectedRevision)
			if err != nil {
				return nil, err
			}
			if !changed {
				return nil, fmt.Errorf("scenario schedule %q changed or was deleted; reload and retry", schedule.ID)
			}
			if existing == nil {
				scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
			} else {
				for i, candidate := range scenario.Schedules {
					if candidate != nil && candidate.ID == schedule.ID {
						scenario.Schedules[i] = cloneScenarioSchedule(schedule)
						break
					}
				}
			}
			return cloneScenarioSchedule(schedule), nil
		}
	}
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return nil, err
	}
	if approvalToSave != nil {
		if err := tr.saveScenarioScheduleApprovalLocked(approvalToSave); err != nil {
			// Compensate the schedule write so a transient approval-store failure
			// cannot leave a durable PendingApprovalID that points at nothing.
			if existing == nil && tr.scenarioStore != nil {
				if deleteErr := tr.scenarioStore.DeleteScenarioSchedule(schedule.ID); deleteErr != nil {
					return nil, errors.Join(err, fmt.Errorf("compensating schedule delete failed: %w", deleteErr))
				}
				for i, candidate := range scenario.Schedules {
					if candidate != nil && candidate.ID == schedule.ID {
						scenario.Schedules = append(scenario.Schedules[:i], scenario.Schedules[i+1:]...)
						break
					}
				}
			} else if existing != nil {
				rollback := cloneScenarioSchedule(existing)
				rollback.UpdatedAt = time.Now().UTC()
				if rollbackErr := tr.saveScenarioScheduleLocked(rollback); rollbackErr != nil {
					return nil, errors.Join(err, fmt.Errorf("compensating schedule rollback failed: %w", rollbackErr))
				}
			}
			return nil, err
		}
	}
	return cloneScenarioSchedule(schedule), nil
}

func (tr *TestRunner) buildScenarioScheduleApproval(schedule *storage.ScenarioSchedule, now time.Time, requestedBy string) *storage.ScenarioScheduleApproval {
	return &storage.ScenarioScheduleApproval{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        normalizeProjectID(schedule.ProjectID),
		ScheduleRevision: schedule.Revision,
		Policy:           schedule.ApprovalPolicy,
		Status:           "pending",
		RequestedBy:      requestedBy,
		RequestedAt:      now,
		RequiredRole:     RoleProjectAdmin,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func (tr *TestRunner) saveScenarioScheduleApprovalLocked(approval *storage.ScenarioScheduleApproval) error {
	if approval == nil {
		return fmt.Errorf("schedule approval is required")
	}
	if tr.scenarioStore != nil {
		return tr.scenarioStore.SaveScenarioScheduleApproval(approval)
	}
	return nil
}

func (tr *TestRunner) ResolveScenarioScheduleApproval(scenarioID string, scheduleID string, approvalID string, approved bool, resolvedBy string, reason string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, nil, false, nil
	}
	var schedule *storage.ScenarioSchedule
	for _, candidate := range scenario.Schedules {
		if candidate != nil && candidate.ID == scheduleID {
			schedule = cloneScenarioSchedule(candidate)
			break
		}
	}
	if schedule == nil {
		return nil, nil, false, nil
	}
	if tr.scenarioStore == nil {
		return nil, nil, false, fmt.Errorf("schedule approval store is not configured")
	}
	approval, err := tr.scenarioStore.GetScenarioScheduleApproval(approvalID)
	if err != nil {
		return nil, nil, false, err
	}
	if approval == nil || approval.ScheduleID != scheduleID {
		return nil, nil, false, nil
	}
	now := time.Now().UTC()
	approval.ResolvedBy = strings.TrimSpace(resolvedBy)
	approval.ResolvedAt = &now
	approval.DecisionReason = strings.TrimSpace(reason)
	approval.UpdatedAt = now
	if approved {
		approval.Status = "approved"
		schedule.PendingApprovalID = ""
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	} else {
		approval.Status = "rejected"
		schedule.PendingApprovalID = ""
		schedule.NextRequiredAction = "approval_rejected"
		schedule.LastError = "schedule approval rejected"
	}
	schedule.UpdatedAt = now
	if atomicStore, ok := tr.scenarioStore.(atomicScenarioScheduleApprovalStore); ok {
		changed, err := atomicStore.ResolveScenarioScheduleApproval(approval, schedule, schedule.Revision)
		if err != nil {
			return nil, nil, false, err
		}
		if !changed {
			return nil, nil, false, fmt.Errorf("schedule approval was already resolved or the schedule changed concurrently")
		}
		for index, current := range scenario.Schedules {
			if current != nil && current.ID == schedule.ID {
				scenario.Schedules[index] = cloneScenarioSchedule(schedule)
				break
			}
		}
	} else {
		if err := tr.scenarioStore.SaveScenarioScheduleApproval(approval); err != nil {
			return nil, nil, false, err
		}
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
			return nil, nil, false, err
		}
	}
	return approval, cloneScenarioSchedule(schedule), true, nil
}

func normalizeScenarioScheduleApprovalPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto"
	case "manual", "manual_review", "high_cost", "off_hours", "production", "quota_burst":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "manual"
	}
}

func normalizeScenarioScheduleMissedRunPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "skip":
		return "skip"
	case "catch_up_once", "catch_up_all", "require_operator_decision":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "skip"
	}
}

func (tr *TestRunner) scenarioScheduleConflictSummaryLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario) string {
	if schedule == nil || schedule.Governance == nil {
		return ""
	}
	findings := make([]string, 0)
	if schedule.Governance.QuotaReview.Decision == "exceeds_project_vuser_limit" {
		findings = append(findings, strings.Join(schedule.Governance.QuotaReview.Findings, " "))
	}
	findings = append(findings, tr.scenarioScheduleReleaseConflictFindingsLocked(schedule)...)
	findings = append(findings, tr.scenarioScheduleCapacityConflictFindingsLocked(schedule, scenario)...)
	return strings.Join(compactScheduleConflictFindings(findings), " ")
}

// scenarioScheduleCapacityConflictFindingsLocked computes the standing
// quota/capacity findings for a candidate schedule's scenario footprint by
// building a non-registered preview test — independent of governance state, so it
// is reusable by both the governance-time summary and the pre-save preview.
func (tr *TestRunner) scenarioScheduleCapacityConflictFindingsLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario) []string {
	if schedule == nil || scenario == nil {
		return nil
	}
	preview, err := tr.buildManagedTestLocked(CreateTestRequest{
		Name:       schedule.Name,
		ScenarioID: scenario.ID,
		ReleaseID:  schedule.ReleaseID,
	}, "schedule-conflict-preview", "DRAFT")
	if err != nil || preview == nil {
		return nil
	}
	findings := make([]string, 0, 2)
	if blocked, reason := tr.evaluateQuotaBlockLocked(preview, ""); blocked {
		findings = append(findings, "Quota conflict: "+reason+".")
	}
	if preview.CapacityAdvisory != nil && preview.CapacityAdvisory.RequiresOverride {
		findings = append(findings, fmt.Sprintf("Capacity pool conflict: recommended %d generator%s but only %d available.", preview.CapacityAdvisory.RecommendedGenerators, pluralSuffix(preview.CapacityAdvisory.RecommendedGenerators), preview.CapacityAdvisory.AvailableGenerators))
	}
	return findings
}

// previewScheduleConflictSummaryLocked is the pre-save equivalent of
// scenarioScheduleConflictSummaryLocked for a not-yet-persisted candidate (no
// governance record yet): it reports the release + quota/capacity conflicts the
// schedule would hit, so the form can warn before the schedule is saved.
func (tr *TestRunner) previewScheduleConflictSummaryLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario) string {
	findings := tr.scenarioScheduleReleaseConflictFindingsLocked(schedule)
	findings = append(findings, tr.scenarioScheduleCapacityConflictFindingsLocked(schedule, scenario)...)
	return strings.Join(compactScheduleConflictFindings(findings), " ")
}

func (tr *TestRunner) scenarioScheduleReleaseConflictFindingsLocked(schedule *storage.ScenarioSchedule) []string {
	if tr == nil || tr.releaseStore == nil || schedule == nil || strings.TrimSpace(schedule.ReleaseID) == "" {
		return nil
	}
	releaseID := strings.TrimSpace(schedule.ReleaseID)
	release, err := tr.releaseStore.GetRelease(releaseID)
	if err != nil || release == nil {
		return []string{fmt.Sprintf("Release conflict: release %s could not be resolved.", releaseID)}
	}
	if release.ArchivedAt != nil {
		return []string{fmt.Sprintf("Release conflict: release %s is archived.", firstNonEmpty(release.Name, release.ID))}
	}
	return nil
}

func compactScheduleConflictFindings(findings []string) []string {
	out := make([]string, 0, len(findings))
	seen := map[string]struct{}{}
	for _, finding := range findings {
		finding = strings.TrimSpace(finding)
		if finding == "" {
			continue
		}
		if _, exists := seen[finding]; exists {
			continue
		}
		seen[finding] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func scenarioScheduleNextRequiredAction(schedule *storage.ScenarioSchedule) string {
	if schedule == nil || !schedule.Enabled {
		return ""
	}
	if strings.TrimSpace(schedule.ConflictSummary) != "" {
		return "resolve_conflict"
	}
	if schedule.ApprovalPolicy != "" && schedule.ApprovalPolicy != "auto" {
		return "approval_required"
	}
	return ""
}

func (tr *TestRunner) scenarioScheduleGovernanceLocked(scenario *StoredScenario, schedule *storage.ScenarioSchedule, req CreateScenarioScheduleRequest, now time.Time) *storage.ScenarioScheduleGovernance {
	projectID := storage.DefaultProjectID
	if scenario != nil {
		projectID = normalizeProjectID(scenario.ProjectID)
	}
	reviewer := strings.TrimSpace(req.OwnerSubject)
	if reviewer == "" && schedule != nil {
		reviewer = strings.TrimSpace(schedule.OwnerSubject)
	}
	reviewSource := strings.TrimSpace(req.RBACReviewSource)
	if reviewSource == "" {
		reviewSource = "api"
	}
	estimatedPeak := 0
	if scenario != nil {
		for _, script := range scenario.Scripts {
			estimatedPeak += script.VUsers
		}
	}
	maxConcurrent := tr.projectMaxConcurrentTests(projectID)
	maxVUsers := tr.projectMaxVUsers(projectID)
	findings := []string{"Schedule mutation authorized for scenario project scope."}
	quotaDecision := "allowed"
	if maxConcurrent > 0 {
		findings = append(findings, fmt.Sprintf("Project allows %d concurrent test(s).", maxConcurrent))
	}
	if maxVUsers > 0 {
		findings = append(findings, fmt.Sprintf("Project allows %d active VUser(s).", maxVUsers))
		if estimatedPeak > maxVUsers {
			quotaDecision = "exceeds_project_vuser_limit"
			findings = append(findings, fmt.Sprintf("Scheduled scenario peak %d VUsers exceeds project limit.", estimatedPeak))
		}
	}
	if maxConcurrent == 0 && maxVUsers == 0 {
		findings = append(findings, "No project-specific quota limit configured; global capacity controls still apply at materialization.")
	}
	return &storage.ScenarioScheduleGovernance{
		PolicyVersion: "schedule-governance.v1",
		RBACReview: storage.ScenarioScheduleRBACReview{
			Decision:      "allowed",
			ReviewedAt:    now,
			ReviewedBy:    reviewer,
			RequiredRoles: []string{RoleEditor, RoleProjectAdmin},
			ReviewSource:  reviewSource,
		},
		QuotaReview: storage.ScenarioScheduleQuotaReview{
			Decision:             quotaDecision,
			ProjectMaxConcurrent: maxConcurrent,
			ProjectMaxVUsers:     maxVUsers,
			EstimatedPeakVUsers:  estimatedPeak,
			Findings:             findings,
		},
	}
}
