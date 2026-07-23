package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type stubObjectRepositoryService struct {
	objectRepositoryService
	configured          bool
	repository          *storage.ObjectRepository
	getCalls            int
	listed              []*storage.ObjectRepository
	listProjectID       string
	listLimit           int
	listOffset          int
	listCalls           int
	updateErr           error
	updateCalls         int
	script              *StoredScript
	scriptFound         bool
	scriptCalls         int
	saved               *storage.ObjectRepository
	saveCalls           int
	updatedScript       *dsl.Script
	updateScriptErr     error
	updateScriptCalls   int
	deleteResult        bool
	deleteErr           error
	deleteCalls         int
	deletedRepositoryID string
}

func (service *stubObjectRepositoryService) StorageConfigured() bool { return service.configured }

func (service *stubObjectRepositoryService) GetRepository(string) (*storage.ObjectRepository, error) {
	service.getCalls++
	return service.repository, nil
}

func (service *stubObjectRepositoryService) SaveRepository(repository *storage.ObjectRepository) error {
	service.saveCalls++
	service.saved = repository
	return nil
}

func (service *stubObjectRepositoryService) ListRepositories(projectID string, limit, offset int) ([]*storage.ObjectRepository, error) {
	service.listCalls++
	service.listProjectID = projectID
	service.listLimit = limit
	service.listOffset = offset
	return service.listed, nil
}

func (service *stubObjectRepositoryService) UpdateRepositoryIfUnchanged(*storage.ObjectRepository, time.Time) (bool, error) {
	service.updateCalls++
	return false, service.updateErr
}

func (service *stubObjectRepositoryService) DeleteRepositoryIfUnreferenced(repository *storage.ObjectRepository) (bool, error) {
	service.deleteCalls++
	if repository != nil {
		service.deletedRepositoryID = repository.ID
	}
	return service.deleteResult, service.deleteErr
}

func (service *stubObjectRepositoryService) GetScript(string) (*StoredScript, bool) {
	service.scriptCalls++
	return service.script, service.scriptFound
}

func (service *stubObjectRepositoryService) UpdateScript(_ string, script *dsl.Script, _ string) (bool, error) {
	service.updateScriptCalls++
	service.updatedScript = script
	return false, service.updateScriptErr
}

type stubCorrelationRulesetService struct {
	correlationRulesetService
	configured    bool
	ruleset       *storage.CorrelationRuleset
	getCalls      int
	listed        []*storage.CorrelationRuleset
	listProjectID string
	listLimit     int
	listOffset    int
	listCalls     int
	updateErr     error
	updateCalls   int
}

func (service *stubCorrelationRulesetService) StorageConfigured() bool { return service.configured }

func (service *stubCorrelationRulesetService) GetRuleset(string) (*storage.CorrelationRuleset, error) {
	service.getCalls++
	return service.ruleset, nil
}

func (service *stubCorrelationRulesetService) ListRulesets(projectID string, limit, offset int) ([]*storage.CorrelationRuleset, error) {
	service.listCalls++
	service.listProjectID = projectID
	service.listLimit = limit
	service.listOffset = offset
	return service.listed, nil
}

func (service *stubCorrelationRulesetService) UpdateRulesetIfUnchanged(*storage.CorrelationRuleset, time.Time) (bool, error) {
	service.updateCalls++
	return false, service.updateErr
}

func TestAuthoringContentListHandlersDelegateToServices(t *testing.T) {
	objectService := &stubObjectRepositoryService{
		configured: true,
		listed:     []*storage.ObjectRepository{{ID: "repo-1", ProjectID: "project-a", Name: "Objects"}},
	}
	server := &Server{objectRepositoryService: objectService}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/object-repositories?project_id=project-a&limit=7&offset=2", nil)
	response := httptest.NewRecorder()
	server.handleListObjectRepositories(response, request)
	if response.Code != http.StatusOK || objectService.listCalls != 1 {
		t.Fatalf("object list response = %d, calls = %d, body=%s", response.Code, objectService.listCalls, response.Body.String())
	}
	if objectService.listProjectID != "project-a" || objectService.listLimit != 7 || objectService.listOffset != 2 {
		t.Fatalf("object list delegation = project:%q limit:%d offset:%d", objectService.listProjectID, objectService.listLimit, objectService.listOffset)
	}

	correlationService := &stubCorrelationRulesetService{
		configured: true,
		listed:     []*storage.CorrelationRuleset{{ID: "rules-1", ProjectID: "project-b", Name: "Rules"}},
	}
	server = &Server{correlationRulesetService: correlationService}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/correlation-rulesets?project_id=project-b&limit=9&offset=3", nil)
	response = httptest.NewRecorder()
	server.handleListCorrelationRulesets(response, request)
	if response.Code != http.StatusOK || correlationService.listCalls != 1 {
		t.Fatalf("ruleset list response = %d, calls = %d, body=%s", response.Code, correlationService.listCalls, response.Body.String())
	}
	if correlationService.listProjectID != "project-b" || correlationService.listLimit != 9 || correlationService.listOffset != 3 {
		t.Fatalf("ruleset list delegation = project:%q limit:%d offset:%d", correlationService.listProjectID, correlationService.listLimit, correlationService.listOffset)
	}
}

func TestAuthoringContentHandlersPreserveUnsupportedCASErrors(t *testing.T) {
	t.Run("object repository", func(t *testing.T) {
		service := &stubObjectRepositoryService{
			configured: true,
			repository: &storage.ObjectRepository{ID: "repo-1", ProjectID: "project-a", Name: "Original"},
			updateErr:  errObjectRepositoryOptimisticUpdateUnsupported,
		}
		server := &Server{objectRepositoryService: service}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/object-repositories/repo-1", bytes.NewBufferString(`{"name":"Updated"}`))
		request.SetPathValue("id", "repo-1")
		response := httptest.NewRecorder()
		server.handleUpdateObjectRepository(response, request)
		if response.Code != http.StatusNotImplemented || service.getCalls != 1 || service.updateCalls != 1 {
			t.Fatalf("object update response = %d, get/update = %d/%d, body=%s", response.Code, service.getCalls, service.updateCalls, response.Body.String())
		}
	})

	t.Run("correlation ruleset", func(t *testing.T) {
		now := time.Now().UTC()
		service := &stubCorrelationRulesetService{
			configured: true,
			ruleset:    &storage.CorrelationRuleset{ID: "rules-1", ProjectID: "project-a", Name: "Original", UpdatedAt: now},
			updateErr:  errCorrelationRulesetOptimisticUpdateUnsupported,
		}
		server := &Server{correlationRulesetService: service}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/correlation-rulesets/rules-1", bytes.NewBufferString(`{"name":"Updated","expected_updated_at":"`+now.Format(time.RFC3339Nano)+`"}`))
		request.SetPathValue("id", "rules-1")
		response := httptest.NewRecorder()
		server.handleUpdateCorrelationRuleset(response, request)
		if response.Code != http.StatusNotImplemented || service.getCalls != 1 || service.updateCalls != 1 {
			t.Fatalf("ruleset update response = %d, get/update = %d/%d, body=%s", response.Code, service.getCalls, service.updateCalls, response.Body.String())
		}
	})
}

func TestObjectRepositoryBindingConflictRollsBackThroughServiceFence(t *testing.T) {
	service := &stubObjectRepositoryService{
		configured:  true,
		scriptFound: true,
		script: &StoredScript{
			ID: "script-1", Name: "Checkout", ProjectID: "project-a",
			Script: &dsl.Script{Actions: []dsl.Action{{Name: "Submit", Type: "browser_click", Selector: "#submit"}}},
		},
		updateScriptErr: errScriptConcurrentChange,
		deleteResult:    true,
	}
	server := &Server{objectRepositoryService: service}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories/from-script", bytes.NewBufferString(`{"script_id":"script-1","bind_script":true}`))
	response := httptest.NewRecorder()

	server.handleObjectRepositoryFromScript(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("from-script status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	if service.saveCalls != 1 || service.updateScriptCalls != 1 || service.deleteCalls != 1 {
		t.Fatalf("service calls = save:%d update-script:%d cleanup:%d, want 1/1/1", service.saveCalls, service.updateScriptCalls, service.deleteCalls)
	}
	if service.saved == nil || service.deletedRepositoryID != service.saved.ID {
		t.Fatalf("cleanup repository = %q, saved = %#v", service.deletedRepositoryID, service.saved)
	}
	if service.updatedScript == nil || service.updatedScript.Actions[0].ObjectRef == "" {
		t.Fatalf("script binding was not delegated: %#v", service.updatedScript)
	}
}

type authoringContentStoreSpy struct {
	storage.Store
	objectExpectedAt      time.Time
	objectUpdateCalls     int
	objectDeleteID        string
	objectDeleteExpected  time.Time
	objectDeleteCalls     int
	correlationExpectedAt time.Time
	correlationCalls      int
}

func (store *authoringContentStoreSpy) UpdateObjectRepositoryIfUnchanged(_ *storage.ObjectRepository, timeValue time.Time) (bool, error) {
	store.objectUpdateCalls++
	store.objectExpectedAt = timeValue
	return true, nil
}

func (store *authoringContentStoreSpy) DeleteObjectRepositoryIfUnreferencedAndUnchanged(id string, timeValue time.Time) (bool, error) {
	store.objectDeleteCalls++
	store.objectDeleteID = id
	store.objectDeleteExpected = timeValue
	return true, nil
}

func (store *authoringContentStoreSpy) UpdateCorrelationRulesetIfUnchanged(_ *storage.CorrelationRuleset, timeValue time.Time) (bool, error) {
	store.correlationCalls++
	store.correlationExpectedAt = timeValue
	return true, nil
}

func TestAuthoringContentAdaptersPreserveCASFences(t *testing.T) {
	store := &authoringContentStoreSpy{}
	objectService := newTestRunnerObjectRepositoryService(nil, store)
	objectExpected := time.Date(2026, 7, 20, 10, 11, 12, 0, time.UTC)
	updated, err := objectService.UpdateRepositoryIfUnchanged(&storage.ObjectRepository{ID: "repo-1"}, objectExpected)
	if err != nil || !updated || store.objectUpdateCalls != 1 || !store.objectExpectedAt.Equal(objectExpected) {
		t.Fatalf("object CAS = updated:%t err:%v calls:%d expected:%v", updated, err, store.objectUpdateCalls, store.objectExpectedAt)
	}
	deleted, err := objectService.DeleteRepositoryIfUnreferenced(&storage.ObjectRepository{ID: "repo-1", UpdatedAt: objectExpected})
	if err != nil || !deleted || store.objectDeleteCalls != 1 || store.objectDeleteID != "repo-1" || !store.objectDeleteExpected.Equal(objectExpected) {
		t.Fatalf("object delete fence = deleted:%t err:%v id:%q expected:%v", deleted, err, store.objectDeleteID, store.objectDeleteExpected)
	}

	correlationService := newTestRunnerCorrelationRulesetService(nil, store)
	correlationExpected := objectExpected.Add(time.Minute)
	updated, err = correlationService.UpdateRulesetIfUnchanged(&storage.CorrelationRuleset{ID: "rules-1"}, correlationExpected)
	if err != nil || !updated || store.correlationCalls != 1 || !store.correlationExpectedAt.Equal(correlationExpected) {
		t.Fatalf("correlation CAS = updated:%t err:%v calls:%d expected:%v", updated, err, store.correlationCalls, store.correlationExpectedAt)
	}
}

func TestAuthoringContentServicesKeepServerConstructionCompatible(t *testing.T) {
	constructed := NewServer(ServerConfig{MaxConcurrent: 2})
	objectAdapter, objectOK := constructed.objectRepositoryService.(*testRunnerObjectRepositoryService)
	correlationAdapter, correlationOK := constructed.correlationRulesetService.(*testRunnerCorrelationRulesetService)
	if !objectOK || objectAdapter.runner != constructed.runner || objectAdapter.store != constructed.store {
		t.Fatalf("NewServer object repository service = %#v", constructed.objectRepositoryService)
	}
	if !correlationOK || correlationAdapter.runner != constructed.runner || correlationAdapter.store != constructed.store {
		t.Fatalf("NewServer correlation ruleset service = %#v", constructed.correlationRulesetService)
	}

	replacementRunner := NewTestRunner(3, NewHub(), constructed.store)
	constructed.runner = replacementRunner
	objectReplacement, objectOK := constructed.objectRepositoryDomainService().(*testRunnerObjectRepositoryService)
	correlationReplacement, correlationOK := constructed.correlationRulesetDomainService().(*testRunnerCorrelationRulesetService)
	if !objectOK || objectReplacement.runner != replacementRunner || !correlationOK || correlationReplacement.runner != replacementRunner {
		t.Fatalf("replacement services = object:%#v correlation:%#v", objectReplacement, correlationReplacement)
	}

	legacy := &Server{runner: replacementRunner, store: constructed.store}
	if _, ok := legacy.objectRepositoryDomainService().(*testRunnerObjectRepositoryService); !ok {
		t.Fatal("legacy Server literal did not receive object-repository compatibility service")
	}
	if _, ok := legacy.correlationRulesetDomainService().(*testRunnerCorrelationRulesetService); !ok {
		t.Fatal("legacy Server literal did not receive correlation-ruleset compatibility service")
	}
}

func TestAuthoringContentListResponsesRemainJSONObjects(t *testing.T) {
	service := &stubObjectRepositoryService{configured: true}
	server := &Server{objectRepositoryService: service}
	response := httptest.NewRecorder()
	server.handleListObjectRepositories(response, httptest.NewRequest(http.MethodGet, "/api/v1/object-repositories", nil))
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode object list response: %v", err)
	}
	if _, ok := payload["object_repositories"]; !ok {
		t.Fatalf("response keys = %v", payload)
	}
}

var _ objectRepositoryService = (*stubObjectRepositoryService)(nil)
var _ correlationRulesetService = (*stubCorrelationRulesetService)(nil)
var _ storage.Store = (*authoringContentStoreSpy)(nil)
