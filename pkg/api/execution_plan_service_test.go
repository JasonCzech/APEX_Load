package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

type stubExecutionPlanService struct {
	executionPlanService
	configured          bool
	fenceErr            error
	fenceCalls          int
	validateCalls       int
	createCalls         int
	listed              []*storage.ExecutionPlan
	listErr             error
	listProjectID       string
	listLimit           int
	listOffset          int
	listCalls           int
	getRun              *storage.ExecutionPlanRun
	getRunErr           error
	getRunID            string
	getRunCalls         int
	createSchedule      *storage.ExecutionPlanSchedule
	createScheduleErr   error
	createScheduleID    string
	createScheduleCalls int
	approval            *storage.ExecutionPlanApproval
	approvalFound       bool
	approvalErr         error
	approvalCalls       int
}

func (service *stubExecutionPlanService) StorageConfigured() bool {
	return service.configured
}

func (service *stubExecutionPlanService) CaptureMutationFence() (executionPlanMutationFence, error) {
	service.fenceCalls++
	return executionPlanMutationFence{}, service.fenceErr
}

func (service *stubExecutionPlanService) Validate(*storage.ExecutionPlan) (int, string) {
	service.validateCalls++
	return 0, ""
}

func (service *stubExecutionPlanService) Create(*storage.ExecutionPlan, executionPlanMutationFence) error {
	service.createCalls++
	return nil
}

func (service *stubExecutionPlanService) List(projectID string, limit, offset int) ([]*storage.ExecutionPlan, error) {
	service.listCalls++
	service.listProjectID = projectID
	service.listLimit = limit
	service.listOffset = offset
	return service.listed, service.listErr
}

func (service *stubExecutionPlanService) GetRun(runID string) (*storage.ExecutionPlanRun, error) {
	service.getRunCalls++
	service.getRunID = runID
	return service.getRun, service.getRunErr
}

func (service *stubExecutionPlanService) CreateSchedule(planID string, _ CreateExecutionPlanScheduleRequest, _ testCreationActor) (*storage.ExecutionPlanSchedule, error) {
	service.createScheduleCalls++
	service.createScheduleID = planID
	return service.createSchedule, service.createScheduleErr
}

func (service *stubExecutionPlanService) ResolveApproval(string, string, bool, string, string) (*storage.ExecutionPlanApproval, bool, error) {
	service.approvalCalls++
	return service.approval, service.approvalFound, service.approvalErr
}

func TestExecutionPlanListHandlerDelegatesToDomainService(t *testing.T) {
	service := &stubExecutionPlanService{
		configured: true,
		listed:     []*storage.ExecutionPlan{{ID: "plan-1", ProjectID: "project-a", Name: "Plan"}},
	}
	server := &Server{executionPlanService: service}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/execution-plans?project_id=project-a&limit=7&offset=2", nil)
	response := httptest.NewRecorder()

	server.handleListExecutionPlans(response, request)

	if response.Code != http.StatusOK || service.listCalls != 1 {
		t.Fatalf("list response = %d, calls = %d, body=%s; want 200 and one service call", response.Code, service.listCalls, response.Body.String())
	}
	if service.listProjectID != "project-a" || service.listLimit != 7 || service.listOffset != 2 {
		t.Fatalf("list delegation = project:%q limit:%d offset:%d", service.listProjectID, service.listLimit, service.listOffset)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if payload.Count != 1 {
		t.Fatalf("list count = %d, want 1", payload.Count)
	}
}

func TestExecutionPlanCreatePreservesFenceBeforeBodyDecode(t *testing.T) {
	service := &stubExecutionPlanService{configured: true, fenceErr: errNotExecutionLeader}
	server := &Server{executionPlanService: service}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans", bytes.NewBufferString(`{"name":`))
	response := httptest.NewRecorder()

	server.handleCreateExecutionPlan(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if service.fenceCalls != 1 || service.validateCalls != 0 || service.createCalls != 0 {
		t.Fatalf("service calls = fence:%d validate:%d create:%d, want 1/0/0", service.fenceCalls, service.validateCalls, service.createCalls)
	}
}

func TestExecutionPlanHandlersPreserveServiceErrorMapping(t *testing.T) {
	t.Run("missing schedule parent", func(t *testing.T) {
		service := &stubExecutionPlanService{configured: true, createScheduleErr: errExecutionPlanNotFound}
		server := &Server{executionPlanService: service, runner: NewTestRunner(1, NewHub(), nil)}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans/missing/schedules", bytes.NewBufferString(`{"cron_expr":"0 9 * * *"}`))
		request.SetPathValue("id", "missing")
		response := httptest.NewRecorder()

		server.handleCreateExecutionPlanSchedule(response, request)

		if response.Code != http.StatusNotFound || service.createScheduleCalls != 1 || service.createScheduleID != "missing" {
			t.Fatalf("schedule response = %d, calls = %d, plan = %q, body=%s", response.Code, service.createScheduleCalls, service.createScheduleID, response.Body.String())
		}
	})

	t.Run("approval conflict", func(t *testing.T) {
		service := &stubExecutionPlanService{
			configured:    true,
			approval:      &storage.ExecutionPlanApproval{ID: "approval-1", PlanID: "plan-1"},
			approvalFound: true,
			approvalErr:   errors.New("approval is already approved"),
		}
		server := &Server{executionPlanService: service, runner: NewTestRunner(1, NewHub(), nil)}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans/plan-1/approvals/approval-1/approve", nil)
		request.SetPathValue("id", "plan-1")
		request.SetPathValue("approval_id", "approval-1")
		response := httptest.NewRecorder()

		server.handleApproveExecutionPlanApproval(response, request)

		if response.Code != http.StatusConflict || service.approvalCalls != 1 {
			t.Fatalf("approval response = %d, calls = %d, body=%s", response.Code, service.approvalCalls, response.Body.String())
		}
	})

	t.Run("run confined to parent plan", func(t *testing.T) {
		service := &stubExecutionPlanService{
			configured: true,
			getRun:     &storage.ExecutionPlanRun{ID: "run-1", PlanID: "other-plan"},
		}
		server := &Server{executionPlanService: service}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/execution-plans/plan-1/runs/run-1", nil)
		request.SetPathValue("id", "plan-1")
		request.SetPathValue("runId", "run-1")
		response := httptest.NewRecorder()

		server.handleGetExecutionPlanRun(response, request)

		if response.Code != http.StatusNotFound || service.getRunCalls != 1 || service.getRunID != "run-1" {
			t.Fatalf("run response = %d, calls = %d, run = %q, body=%s", response.Code, service.getRunCalls, service.getRunID, response.Body.String())
		}
	})
}

func TestExecutionPlanServiceKeepsServerConstructionCompatible(t *testing.T) {
	constructed := NewServer(ServerConfig{MaxConcurrent: 2})
	adapter, ok := constructed.executionPlanService.(*testRunnerExecutionPlanService)
	if !ok || adapter.runner != constructed.runner || adapter.store != constructed.store {
		t.Fatalf("NewServer execution-plan service = %#v, want runner/store compatibility adapter", constructed.executionPlanService)
	}

	replacementRunner := NewTestRunner(3, NewHub(), constructed.store)
	constructed.runner = replacementRunner
	replacement, ok := constructed.executionPlanDomainService().(*testRunnerExecutionPlanService)
	if !ok || replacement.runner != replacementRunner || replacement.store != constructed.store {
		t.Fatalf("replacement execution-plan service = %#v, want current runner/store", replacement)
	}

	legacy := &Server{runner: replacementRunner, store: constructed.store}
	compatibility, ok := legacy.executionPlanDomainService().(*testRunnerExecutionPlanService)
	if !ok || compatibility.runner != replacementRunner || compatibility.store != constructed.store {
		t.Fatalf("legacy Server literal execution-plan service = %#v", compatibility)
	}
}

var _ executionPlanService = (*stubExecutionPlanService)(nil)
