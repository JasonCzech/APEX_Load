package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// executionPlanMutationFence is an opaque request-scoped write token. Capturing
// it before decoding or loading mutable data preserves the existing HA fencing
// semantics across the HTTP/service boundary.
type executionPlanMutationFence struct {
	leaseTerm executionLeaseTerm
}

// executionPlanService is the narrow domain boundary used by execution-plan
// HTTP handlers. It owns durable plan reads, fenced mutations, materialization,
// schedules, occurrences, and approvals while the Server remains responsible
// for transport concerns and audit emission.
type executionPlanService interface {
	StorageConfigured() bool
	CaptureMutationFence() (executionPlanMutationFence, error)
	Validate(*storage.ExecutionPlan) (int, string)
	Create(*storage.ExecutionPlan, executionPlanMutationFence) error
	List(string, int, int) ([]*storage.ExecutionPlan, error)
	Get(string) (*storage.ExecutionPlan, error)
	Update(*storage.ExecutionPlan, int64, executionPlanMutationFence) error
	Delete(string, executionPlanMutationFence) (storage.ExecutionPlanDeleteResult, string, error)
	MaterializeAndRun(*storage.ExecutionPlan, string, string, testCreationActor) (*ExecutionPlanMaterializeResult, error)
	ListRuns(string, int, int) ([]*storage.ExecutionPlanRun, error)
	GetRun(string) (*storage.ExecutionPlanRun, error)
	CreateSchedule(string, CreateExecutionPlanScheduleRequest, testCreationActor) (*storage.ExecutionPlanSchedule, error)
	ListSchedules(string) ([]*storage.ExecutionPlanSchedule, error)
	UpdateSchedule(string, string, CreateExecutionPlanScheduleRequest, testCreationActor) (*storage.ExecutionPlanSchedule, bool, error)
	DeleteSchedule(string, string) (bool, error)
	ListScheduleOccurrences(string, string, int, int) ([]*storage.ExecutionPlanScheduleOccurrence, bool, error)
	ResolveScheduleOccurrence(string, string, string, ScheduleOccurrenceResolutionRequest) (*storage.ExecutionPlanScheduleOccurrence, *storage.ExecutionPlanSchedule, bool, error)
	ListApprovals(string, int, int) ([]*storage.ExecutionPlanApproval, error)
	ResolveApproval(string, string, bool, string, string) (*storage.ExecutionPlanApproval, bool, error)
}

// testRunnerExecutionPlanService preserves the current TestRunner and storage
// implementation behind the domain interface. This is deliberately a
// compatibility adapter: public constructors and orchestration behavior remain
// unchanged while handlers shed direct dependencies on the god objects.
type testRunnerExecutionPlanService struct {
	runner *TestRunner
	store  storage.Store
}

func newTestRunnerExecutionPlanService(runner *TestRunner, store storage.Store) executionPlanService {
	return &testRunnerExecutionPlanService{runner: runner, store: store}
}

func (service *testRunnerExecutionPlanService) StorageConfigured() bool {
	return service != nil && service.store != nil
}

func (service *testRunnerExecutionPlanService) CaptureMutationFence() (executionPlanMutationFence, error) {
	term, err := service.runner.captureExecutionLeaseTerm()
	return executionPlanMutationFence{leaseTerm: term}, err
}

func (service *testRunnerExecutionPlanService) Validate(plan *storage.ExecutionPlan) (int, string) {
	if plan == nil {
		return http.StatusBadRequest, "execution plan is required"
	}
	if strings.TrimSpace(plan.Name) == "" {
		return http.StatusBadRequest, "execution plan name is required"
	}
	if len(plan.Entries) > maxExecutionPlanEntries {
		return http.StatusBadRequest, fmt.Sprintf("execution plan contains %d entries; limit is %d", len(plan.Entries), maxExecutionPlanEntries)
	}
	if plan.RunMode != "" && !validExecutionRunMode(plan.RunMode) {
		return http.StatusBadRequest, "run_mode must be parallel or sequential"
	}
	planProject := normalizeProjectID(plan.ProjectID)
	for entryIndex := range plan.Entries {
		entry := &plan.Entries[entryIndex]
		if strings.TrimSpace(entry.ScenarioID) == "" {
			return http.StatusBadRequest, "every execution plan entry requires a scenario_id"
		}
		if entry.FailurePolicy != "" && entry.FailurePolicy != "halt" && entry.FailurePolicy != "skip" {
			return http.StatusBadRequest, "entry failure_policy must be halt or skip"
		}
		if len(entry.RuntimeOverrides) > 0 {
			if len(entry.RuntimeOverrides) > maxExecutionPlanRuntimeOverrideBytes {
				return http.StatusBadRequest, fmt.Sprintf("entry runtime_overrides exceeds the %d-byte limit", maxExecutionPlanRuntimeOverrideBytes)
			}
			var runtimeOverrides *RuntimeSettingsOverride
			if err := json.Unmarshal(entry.RuntimeOverrides, &runtimeOverrides); err != nil {
				return http.StatusBadRequest, "entry runtime_overrides must be a valid runtime settings object"
			}
		}
		if len(entry.DataPools) > maxExecutionPlanDataPoolsPerEntry {
			return http.StatusBadRequest, fmt.Sprintf("execution plan entry contains %d data pools; limit is %d", len(entry.DataPools), maxExecutionPlanDataPoolsPerEntry)
		}
		normalizedDataPools, err := normalizeDataPoolBindingIntent(entry.DataPools)
		if err != nil {
			return http.StatusBadRequest, fmt.Sprintf("invalid execution plan entry data pools: %v", err)
		}
		entry.DataPools = normalizedDataPools
		// Cross-tenant guards use durable ownership, not the controller's
		// eventually-consistent HA cache. Unknown scenarios remain valid draft
		// references and fail per-entry at materialization, but an existing
		// scenario or data pool can never be attached across projects.
		if service.store != nil {
			_, scripts, err := getPublishedImportScenario(service.store, entry.ScenarioID)
			switch {
			case err == nil:
				scenarioProject, status, message := resolveProjectIDFromScenarioScripts(scripts, service.store)
				if status != 0 {
					return status, message
				}
				if normalizeProjectID(scenarioProject) != planProject {
					return http.StatusBadRequest, "an execution plan entry references a scenario in a different project"
				}
			case errors.Is(err, sql.ErrNoRows):
				// Draft plans may intentionally name a scenario that will be created later.
			case err != nil:
				return http.StatusInternalServerError, "failed to validate execution plan scenario ownership"
			}
			if err := validateExecutionPlanDataPoolProjects(service.store, planProject, entry.DataPools); err != nil {
				return http.StatusBadRequest, err.Error()
			}
		} else if service.runner != nil {
			if scenario, ok := service.runner.GetScenario(entry.ScenarioID); ok && normalizeProjectID(scenario.ProjectID) != planProject {
				return http.StatusBadRequest, "an execution plan entry references a scenario in a different project"
			}
		}
	}
	return 0, ""
}

func validateExecutionPlanDataPoolProjects(store storage.Store, projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	if store == nil || len(bindings) == 0 {
		return nil
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		pool, err := store.GetDataPool(binding.PoolID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("data pool %q not found", binding.PoolID)
			}
			return fmt.Errorf("load data pool %q: %w", binding.PoolID, err)
		}
		if normalizeProjectID(pool.ProjectID) != expectedProjectID {
			return fmt.Errorf("data pool %q does not belong to scenario project", binding.PoolID)
		}
		if err := errDataPoolArchived(pool); err != nil {
			return err
		}
	}
	return nil
}

func (service *testRunnerExecutionPlanService) Create(plan *storage.ExecutionPlan, fence executionPlanMutationFence) error {
	return service.runner.createExecutionPlanWithTerm(plan, fence.leaseTerm)
}

func (service *testRunnerExecutionPlanService) List(projectID string, limit, offset int) ([]*storage.ExecutionPlan, error) {
	return service.store.ListExecutionPlans(projectID, limit, offset)
}

func (service *testRunnerExecutionPlanService) Get(id string) (*storage.ExecutionPlan, error) {
	return service.store.GetExecutionPlan(id)
}

func (service *testRunnerExecutionPlanService) Update(plan *storage.ExecutionPlan, expectedRevision int64, fence executionPlanMutationFence) error {
	return service.runner.updateExecutionPlanWithTerm(plan, expectedRevision, fence.leaseTerm)
}

func (service *testRunnerExecutionPlanService) Delete(id string, fence executionPlanMutationFence) (storage.ExecutionPlanDeleteResult, string, error) {
	projectID := ""
	if plan, err := service.store.GetExecutionPlan(id); err == nil && plan != nil {
		projectID = plan.ProjectID
	}
	result, err := service.runner.deleteExecutionPlanWithTerm(id, fence.leaseTerm)
	return result, projectID, err
}

func (service *testRunnerExecutionPlanService) MaterializeAndRun(plan *storage.ExecutionPlan, subject, displayName string, actor testCreationActor) (*ExecutionPlanMaterializeResult, error) {
	return service.runner.MaterializeAndRunExecutionPlan(plan, subject, displayName, actor)
}

func (service *testRunnerExecutionPlanService) ListRuns(planID string, limit, offset int) ([]*storage.ExecutionPlanRun, error) {
	return service.store.ListExecutionPlanRuns(planID, limit, offset)
}

func (service *testRunnerExecutionPlanService) GetRun(runID string) (*storage.ExecutionPlanRun, error) {
	return service.store.GetExecutionPlanRun(runID)
}

func (service *testRunnerExecutionPlanService) CreateSchedule(planID string, request CreateExecutionPlanScheduleRequest, actor testCreationActor) (*storage.ExecutionPlanSchedule, error) {
	return service.runner.CreateExecutionPlanSchedule(planID, request, actor)
}

func (service *testRunnerExecutionPlanService) ListSchedules(planID string) ([]*storage.ExecutionPlanSchedule, error) {
	return service.runner.ListExecutionPlanSchedulesForPlan(planID)
}

func (service *testRunnerExecutionPlanService) UpdateSchedule(planID, scheduleID string, request CreateExecutionPlanScheduleRequest, actor testCreationActor) (*storage.ExecutionPlanSchedule, bool, error) {
	return service.runner.UpdateExecutionPlanSchedule(planID, scheduleID, request, actor)
}

func (service *testRunnerExecutionPlanService) DeleteSchedule(planID, scheduleID string) (bool, error) {
	return service.runner.DeleteExecutionPlanScheduleForPlan(planID, scheduleID)
}

func (service *testRunnerExecutionPlanService) ListScheduleOccurrences(planID, scheduleID string, limit, offset int) ([]*storage.ExecutionPlanScheduleOccurrence, bool, error) {
	return service.runner.ListExecutionPlanScheduleOccurrencesPageForPlan(planID, scheduleID, limit, offset)
}

func (service *testRunnerExecutionPlanService) ResolveScheduleOccurrence(planID, scheduleID, occurrenceID string, request ScheduleOccurrenceResolutionRequest) (*storage.ExecutionPlanScheduleOccurrence, *storage.ExecutionPlanSchedule, bool, error) {
	return service.runner.ResolveExecutionPlanScheduleOccurrence(planID, scheduleID, occurrenceID, request)
}

func (service *testRunnerExecutionPlanService) ListApprovals(planID string, limit, offset int) ([]*storage.ExecutionPlanApproval, error) {
	return service.runner.ListExecutionPlanApprovalsPage(planID, limit, offset)
}

func (service *testRunnerExecutionPlanService) ResolveApproval(planID, approvalID string, approve bool, resolvedBy, reason string) (*storage.ExecutionPlanApproval, bool, error) {
	return service.runner.ResolveExecutionPlanApproval(planID, approvalID, approve, resolvedBy, reason)
}

func (s *Server) executionPlanDomainService() executionPlanService {
	if s.executionPlanService != nil {
		if adapter, ok := s.executionPlanService.(*testRunnerExecutionPlanService); ok && (adapter.runner != s.runner || !sameStorageStore(adapter.store, s.store)) {
			// Preserve focused-test compatibility when the historical runner field is
			// replaced after construction without overriding an injected service.
			return newTestRunnerExecutionPlanService(s.runner, s.store)
		}
		return s.executionPlanService
	}
	// Compatibility for tests and internal callers that still construct Server
	// literals with the historical runner/store fields.
	return newTestRunnerExecutionPlanService(s.runner, s.store)
}

// validateExecutionPlan remains as a compatibility facade for focused tests and
// internal callers while validation ownership moves into the domain service.
func (s *Server) validateExecutionPlan(plan *storage.ExecutionPlan) (int, string) {
	return s.executionPlanDomainService().Validate(plan)
}

var _ executionPlanService = (*testRunnerExecutionPlanService)(nil)
