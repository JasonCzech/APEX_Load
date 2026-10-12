package api

// Execution plans inside the workspace. A plan carries an application and
// release, and every run it starts is filed there and linked back to the plan
// run that started it, so a plan's runs appear in the workspace's Command
// Center and results instead of the project's legacy release.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// TestExecutionPlanLink names the execution-plan run that started a run.
type TestExecutionPlanLink struct {
	PlanID    string `json:"plan_id"`
	PlanName  string `json:"plan_name,omitempty"`
	PlanRunID string `json:"plan_run_id"`
	// EntryOrder is the plan entry's stored order; EntryNumber and EntryCount
	// place the run among the plan run's enabled entries ("entry 2 of 3").
	EntryOrder  int `json:"entry_order"`
	EntryNumber int `json:"entry_number,omitempty"`
	EntryCount  int `json:"entry_count,omitempty"`
}

func cloneTestExecutionPlanLink(link *TestExecutionPlanLink) *TestExecutionPlanLink {
	if link == nil {
		return nil
	}
	clone := *link
	return &clone
}

// executionPlanLinkForStorage boxes a link as a true nil interface when absent,
// so a run without a plan never persists a typed-nil that decodes as a link.
func executionPlanLinkForStorage(link *TestExecutionPlanLink) any {
	if link == nil {
		return nil
	}
	return cloneTestExecutionPlanLink(link)
}

func decodeStoredExecutionPlanLink(value any) *TestExecutionPlanLink {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var link TestExecutionPlanLink
	if err := json.Unmarshal(data, &link); err != nil {
		return nil
	}
	if strings.TrimSpace(link.PlanID) == "" || strings.TrimSpace(link.PlanRunID) == "" {
		return nil
	}
	return &link
}

// executionPlanEntryContext is where a plan entry's run is filed and how it
// links back to its plan run.
type executionPlanEntryContext struct {
	planID        string
	planName      string
	runID         string
	applicationID string
	releaseID     string
	entryNumber   int
	entryCount    int
}

// apply files the request under the plan's release and links the run to its
// plan run. An empty release keeps the legacy fallback.
func (ctx executionPlanEntryContext) apply(req *CreateTestRequest, entryOrder int) {
	if req == nil {
		return
	}
	if releaseID := strings.TrimSpace(ctx.releaseID); releaseID != "" {
		req.ReleaseID = releaseID
		req.ApplicationID = strings.TrimSpace(ctx.applicationID)
	}
	if strings.TrimSpace(ctx.planID) == "" || strings.TrimSpace(ctx.runID) == "" {
		return
	}
	req.ExecutionPlan = &TestExecutionPlanLink{
		PlanID:      strings.TrimSpace(ctx.planID),
		PlanName:    strings.TrimSpace(ctx.planName),
		PlanRunID:   strings.TrimSpace(ctx.runID),
		EntryOrder:  entryOrder,
		EntryNumber: ctx.entryNumber,
		EntryCount:  ctx.entryCount,
	}
}

// executionPlanRunEntryContext rebuilds an entry's context from a persisted
// plan-run snapshot (sequential advance and crash recovery).
func executionPlanRunEntryContext(run *storage.ExecutionPlanRun, planName string, index int) executionPlanEntryContext {
	ctx := executionPlanEntryContext{}
	if run == nil || index < 0 || index >= len(run.Entries) {
		return ctx
	}
	entry := run.Entries[index]
	ctx.planID = run.PlanID
	ctx.planName = planName
	ctx.runID = run.ID
	ctx.applicationID = entry.ApplicationID
	ctx.releaseID = entry.ReleaseID
	ctx.entryNumber, ctx.entryCount = executionPlanRunEntryPosition(run.Entries, index)
	return ctx
}

// executionPlanRunEntryPosition numbers an entry among the enabled (not
// skipped-as-disabled) entries of a plan run.
func executionPlanRunEntryPosition(entries []storage.ExecutionPlanRunEntry, index int) (int, int) {
	number, count := 0, 0
	for i, entry := range entries {
		if entry.Status == "skipped" && entry.Detail == "entry disabled" {
			continue
		}
		count++
		if i == index {
			number = count
		}
	}
	return number, count
}

// executionPlanReleaseContext is the release a plan's runs are filed under:
// the schedule's override, else the plan's own.
func executionPlanReleaseContext(plan *storage.ExecutionPlan, schedule *storage.ExecutionPlanSchedule) (string, string) {
	if schedule != nil && strings.TrimSpace(schedule.ReleaseID) != "" {
		return "", strings.TrimSpace(schedule.ReleaseID)
	}
	if plan == nil {
		return "", ""
	}
	return strings.TrimSpace(plan.ApplicationID), strings.TrimSpace(plan.ReleaseID)
}

// executionPlanReleaseStore is the slice of storage needed to check a plan's
// release reference.
type executionPlanReleaseStore interface {
	GetRelease(id string) (*storage.Release, error)
}

var errExecutionPlanReleaseOutsideApplication = errors.New("application_id does not match the release")

// normalizeExecutionPlanRelease checks that a plan's (or plan schedule's)
// release exists in the plan's project and is open, and fills the application
// from the release. An application without a release is rejected, as it is for
// a runnable test.
func normalizeExecutionPlanRelease(store any, projectID string, applicationID, releaseID *string) error {
	application := strings.TrimSpace(*applicationID)
	release := strings.TrimSpace(*releaseID)
	*applicationID, *releaseID = application, release
	if release == "" {
		if application != "" {
			return errors.New("application_id requires a release_id")
		}
		return nil
	}
	releases, ok := store.(executionPlanReleaseStore)
	if !ok || releases == nil {
		return nil
	}
	record, err := releases.GetRelease(release)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("release %q not found", release)
		}
		return fmt.Errorf("load release %q: %w", release, err)
	}
	if record == nil {
		return fmt.Errorf("release %q not found", release)
	}
	if normalizeProjectID(record.ProjectID) != normalizeProjectID(projectID) {
		return fmt.Errorf("release %q does not belong to the plan's project", release)
	}
	if record.ArchivedAt != nil {
		return fmt.Errorf("release %q is archived", release)
	}
	if application != "" && application != strings.TrimSpace(record.ApplicationID) {
		return errExecutionPlanReleaseOutsideApplication
	}
	*applicationID = strings.TrimSpace(record.ApplicationID)
	*releaseID = strings.TrimSpace(record.ID)
	return nil
}

// executionPlanScheduleReleaseStore is the slice of storage needed to check the
// releases a plan's schedules file their runs under.
type executionPlanScheduleReleaseStore interface {
	executionPlanReleaseStore
	ListExecutionPlanSchedules(planID string) ([]*storage.ExecutionPlanSchedule, error)
}

// executionPlanScheduleReleasesOutside names the plan's schedules that file
// their runs under a release of an application other than the plan's. A
// schedule may only choose another release of the plan's application, so a
// plan cannot move away from the application its schedules' releases are in.
func executionPlanScheduleReleasesOutside(store any, plan *storage.ExecutionPlan) ([]string, error) {
	releases, ok := store.(executionPlanScheduleReleaseStore)
	if !ok || releases == nil || plan == nil || strings.TrimSpace(plan.ID) == "" {
		return nil, nil
	}
	schedules, err := releases.ListExecutionPlanSchedules(plan.ID)
	if err != nil {
		return nil, err
	}
	applicationID := strings.TrimSpace(plan.ApplicationID)
	var names []string
	for _, schedule := range schedules {
		if schedule == nil || strings.TrimSpace(schedule.ReleaseID) == "" {
			continue
		}
		release, err := releases.GetRelease(strings.TrimSpace(schedule.ReleaseID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if applicationID == "" || release == nil || strings.TrimSpace(release.ApplicationID) != applicationID {
			names = append(names, fmt.Sprintf("%q", strings.TrimSpace(schedule.Name)))
		}
	}
	return names, nil
}

// normalizeExecutionPlanOverlapPolicy validates a plan's overlap policy.
func normalizeExecutionPlanOverlapPolicy(policy string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(policy)); value {
	case "", storage.ExecutionPlanOverlapSkip, storage.ExecutionPlanOverlapQueue, storage.ExecutionPlanOverlapAllow:
		return value, nil
	default:
		return "", errors.New("overlap_policy must be skip, queue or allow")
	}
}

// effectiveExecutionPlanOverlapPolicy is the policy a plan runs under. Plans
// saved before the policy existed keep starting alongside a running run.
func effectiveExecutionPlanOverlapPolicy(plan *storage.ExecutionPlan) string {
	if plan == nil || strings.TrimSpace(plan.OverlapPolicy) == "" {
		return storage.ExecutionPlanOverlapAllow
	}
	return strings.ToLower(strings.TrimSpace(plan.OverlapPolicy))
}
