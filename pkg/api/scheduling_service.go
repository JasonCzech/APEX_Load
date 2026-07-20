package api

import (
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// schedulingService is the narrow domain boundary used by scenario scheduling
// HTTP handlers. The TestRunner adapter preserves the existing durable/HA
// behavior while keeping handlers from reaching into TestRunner locks, stores,
// and scheduler health state.
type schedulingService interface {
	PreviewScenarioScheduleRuns(scenarioID string, request CreateScenarioScheduleRequest, count int) (*ScenarioSchedulePreview, error)
	CreateScenarioSchedule(scenarioID string, request CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error)
	ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool)
	UpdateScenarioSchedule(scenarioID, scheduleID string, request CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error)
	DeleteScenarioSchedule(scenarioID, scheduleID string) (bool, error)
	ListScenarioScheduleOccurrencesPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleOccurrence, bool, error)
	ResolveScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID string, request ScheduleOccurrenceResolutionRequest) (*storage.ScenarioScheduleOccurrence, *storage.ScenarioSchedule, bool, error)
	DeleteScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID string) (bool, error)
	ListScenarioScheduleApprovalsPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleApproval, bool, error)
	ResolveScenarioScheduleApproval(scenarioID, scheduleID, approvalID string, approved bool, resolvedBy, reason string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error)
	ScenarioProjectID(scenarioID string) string
	Health(now time.Time) ScenarioScheduleHealth
	ArchiveRetentionSettings() ArchiveRetentionSettings
	BuildRunCalendar(from, to time.Time, projectID string, now time.Time, inventory SystemInventoryResponse, calendarRange RunCalendarRange, limit int) RunCalendarResponse
	BuildRunCalendarCapacity(from, to time.Time, projectID, timezone string) *RunCalendarCapacity
}

type testRunnerSchedulingService struct {
	runner    *TestRunner
	scheduler *scenarioScheduler
}

func newTestRunnerSchedulingService(runner *TestRunner, scheduler *scenarioScheduler) schedulingService {
	return &testRunnerSchedulingService{runner: runner, scheduler: scheduler}
}

func (service *testRunnerSchedulingService) PreviewScenarioScheduleRuns(scenarioID string, request CreateScenarioScheduleRequest, count int) (*ScenarioSchedulePreview, error) {
	return service.runner.PreviewScenarioScheduleRuns(scenarioID, request, count)
}

func (service *testRunnerSchedulingService) CreateScenarioSchedule(scenarioID string, request CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	return service.runner.CreateScenarioSchedule(scenarioID, request)
}

func (service *testRunnerSchedulingService) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool) {
	return service.runner.ListScenarioSchedules(scenarioID)
}

func (service *testRunnerSchedulingService) UpdateScenarioSchedule(scenarioID, scheduleID string, request CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error) {
	return service.runner.UpdateScenarioSchedule(scenarioID, scheduleID, request)
}

func (service *testRunnerSchedulingService) DeleteScenarioSchedule(scenarioID, scheduleID string) (bool, error) {
	return service.runner.DeleteScenarioSchedule(scenarioID, scheduleID)
}

func (service *testRunnerSchedulingService) ListScenarioScheduleOccurrencesPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleOccurrence, bool, error) {
	return service.runner.ListScenarioScheduleOccurrencesPage(scenarioID, scheduleID, limit, offset)
}

func (service *testRunnerSchedulingService) ResolveScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID string, request ScheduleOccurrenceResolutionRequest) (*storage.ScenarioScheduleOccurrence, *storage.ScenarioSchedule, bool, error) {
	return service.runner.ResolveScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID, request)
}

func (service *testRunnerSchedulingService) DeleteScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID string) (bool, error) {
	return service.runner.DeleteScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID)
}

func (service *testRunnerSchedulingService) ListScenarioScheduleApprovalsPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleApproval, bool, error) {
	return service.runner.ListScenarioScheduleApprovalsPage(scenarioID, scheduleID, limit, offset)
}

func (service *testRunnerSchedulingService) ResolveScenarioScheduleApproval(scenarioID, scheduleID, approvalID string, approved bool, resolvedBy, reason string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error) {
	return service.runner.ResolveScenarioScheduleApproval(scenarioID, scheduleID, approvalID, approved, resolvedBy, reason)
}

func (service *testRunnerSchedulingService) ScenarioProjectID(scenarioID string) string {
	scenario, ok := service.runner.GetScenario(scenarioID)
	if !ok || scenario == nil {
		return ""
	}
	return scenario.ProjectID
}

// Health composes scenario, execution-plan, one-time, and worker-sweep health in
// one service-owned snapshot. In particular, the runner lock is never exposed to
// the HTTP handler.
func (service *testRunnerSchedulingService) Health(now time.Time) ScenarioScheduleHealth {
	now = now.UTC()
	health := service.runner.ScenarioScheduleHealth(now)
	health.IsLeader = service.runner.isExecutionLeader()
	if service.scheduler != nil {
		health.Sweeps = service.scheduler.healthSnapshot()
	}
	if store := service.runner.execPlanScheduleStore; store != nil {
		schedules, err := store.ListAllExecutionPlanSchedules()
		if err != nil {
			health.StorageError = firstNonEmpty(health.StorageError, "execution-plan schedules unavailable: "+err.Error())
		} else {
			for _, schedule := range schedules {
				if schedule == nil {
					continue
				}
				health.ExecutionPlanTotal++
				if schedule.Enabled {
					health.ExecutionPlanEnabled++
				}
				if schedule.NextRunAt != nil && !schedule.NextRunAt.After(now) {
					health.ExecutionPlanDue++
				}
				if schedule.NextRequiredAction == "operator_decision_required" || schedule.LastError != "" {
					health.ExecutionPlanBlocked++
				}
			}
		}
	}
	service.runner.mu.RLock()
	for _, test := range service.runner.tests {
		if test == nil || test.Status != "SCHEDULED" {
			continue
		}
		health.OneTimeScheduled++
		if test.ScheduledStartAt != nil && !test.ScheduledStartAt.After(now) {
			health.OneTimeDue++
		}
	}
	service.runner.mu.RUnlock()
	return health
}

func (service *testRunnerSchedulingService) ArchiveRetentionSettings() ArchiveRetentionSettings {
	return service.runner.GetSystemSettings().ArchiveRetention
}

func (service *testRunnerSchedulingService) BuildRunCalendar(from, to time.Time, projectID string, now time.Time, inventory SystemInventoryResponse, calendarRange RunCalendarRange, limit int) RunCalendarResponse {
	return service.runner.BuildRunCalendar(from, to, projectID, now, inventory, calendarRange, limit)
}

func (service *testRunnerSchedulingService) BuildRunCalendarCapacity(from, to time.Time, projectID, timezone string) *RunCalendarCapacity {
	return service.runner.buildRunCalendarCapacity(from, to, projectID, timezone)
}

func (s *Server) schedulingDomainService() schedulingService {
	if s.schedulingService != nil {
		if adapter, ok := s.schedulingService.(*testRunnerSchedulingService); ok && (adapter.runner != s.runner || adapter.scheduler != s.scheduler) {
			// Preserve focused-test compatibility when historical fields are replaced
			// after construction, without overriding an explicitly injected service.
			return newTestRunnerSchedulingService(s.runner, s.scheduler)
		}
		return s.schedulingService
	}
	// Compatibility for tests and internal callers that still construct Server
	// literals with the historical runner/scheduler fields.
	return newTestRunnerSchedulingService(s.runner, s.scheduler)
}

var _ schedulingService = (*testRunnerSchedulingService)(nil)
