package api

import (
	"errors"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errObjectRepositoryOptimisticUpdateUnsupported = errors.New("optimistic object repository updates are not supported by this storage backend")
var errObjectRepositoryGuardedDeleteUnsupported = errors.New("guarded object repository deletion is not supported by this storage backend")
var errCorrelationRulesetOptimisticUpdateUnsupported = errors.New("optimistic correlation ruleset updates are not supported by this storage backend")
var errCorrelationRulesetGuardedDeleteUnsupported = errors.New("guarded correlation ruleset deletion is not supported by this storage backend")

// objectRepositoryService is the narrow domain boundary used by object-
// repository HTTP handlers. The compatibility adapter retains the existing
// TestRunner/storage behavior, including durable script CAS and guarded
// repository cleanup after a failed bind.
type objectRepositoryService interface {
	StorageConfigured() bool
	GetRepository(string) (*storage.ObjectRepository, error)
	SaveRepository(*storage.ObjectRepository) error
	ListRepositories(string, int, int) ([]*storage.ObjectRepository, error)
	UpdateRepositoryIfUnchanged(*storage.ObjectRepository, time.Time) (bool, error)
	DeleteRepositoryIfUnreferenced(*storage.ObjectRepository) (bool, error)
	GetScript(string) (*StoredScript, bool)
	UpdateScript(string, *dsl.Script, string) (bool, error)
}

type testRunnerObjectRepositoryService struct {
	runner *TestRunner
	store  storage.Store
}

func newTestRunnerObjectRepositoryService(runner *TestRunner, store storage.Store) objectRepositoryService {
	return &testRunnerObjectRepositoryService{runner: runner, store: store}
}

func (service *testRunnerObjectRepositoryService) StorageConfigured() bool {
	return service != nil && service.store != nil
}

func (service *testRunnerObjectRepositoryService) GetRepository(id string) (*storage.ObjectRepository, error) {
	return service.store.GetObjectRepository(id)
}

func (service *testRunnerObjectRepositoryService) SaveRepository(repository *storage.ObjectRepository) error {
	return service.store.SaveObjectRepository(repository)
}

func (service *testRunnerObjectRepositoryService) ListRepositories(projectID string, limit, offset int) ([]*storage.ObjectRepository, error) {
	return service.store.ListObjectRepositories(projectID, limit, offset)
}

func (service *testRunnerObjectRepositoryService) UpdateRepositoryIfUnchanged(repository *storage.ObjectRepository, expectedUpdatedAt time.Time) (bool, error) {
	store, ok := service.store.(objectRepositoryCASPersistence)
	if !ok {
		return false, errObjectRepositoryOptimisticUpdateUnsupported
	}
	return store.UpdateObjectRepositoryIfUnchanged(repository, expectedUpdatedAt)
}

func (service *testRunnerObjectRepositoryService) DeleteRepositoryIfUnreferenced(repository *storage.ObjectRepository) (bool, error) {
	if repository == nil {
		return false, nil
	}
	store, ok := service.store.(interface {
		DeleteObjectRepositoryIfUnreferencedAndUnchanged(string, time.Time) (bool, error)
	})
	if !ok {
		return false, errObjectRepositoryGuardedDeleteUnsupported
	}
	return store.DeleteObjectRepositoryIfUnreferencedAndUnchanged(repository.ID, repository.UpdatedAt)
}

func (service *testRunnerObjectRepositoryService) GetScript(id string) (*StoredScript, bool) {
	return service.runner.GetScript(id)
}

func (service *testRunnerObjectRepositoryService) UpdateScript(id string, script *dsl.Script, projectID string) (bool, error) {
	_, updated, err := service.runner.UpdateScriptDurableInProject(id, script, projectID)
	return updated, err
}

// correlationRulesetService is the narrow domain boundary used by correlation-
// ruleset handlers. It preserves baseline reads and durable script mutation CAS
// while hiding the full Server store and TestRunner from the transport layer.
type correlationRulesetService interface {
	StorageConfigured() bool
	GetRuleset(string) (*storage.CorrelationRuleset, error)
	SaveRuleset(*storage.CorrelationRuleset) error
	ListRulesets(string, int, int) ([]*storage.CorrelationRuleset, error)
	UpdateRulesetIfUnchanged(*storage.CorrelationRuleset, time.Time) (bool, error)
	DeleteRulesetIfUnchanged(*storage.CorrelationRuleset, string) (bool, error)
	GetScript(string) (*StoredScript, bool)
	GetScriptRecording(string) (*storage.ScriptRecordingRecord, error)
	UpdateScriptIfUnchangedAndRulesetCurrent(
		string,
		*dsl.Script,
		string,
		time.Time,
		string,
		string,
		time.Time,
	) (bool, error)
}

type correlationRulesetCASPersistence interface {
	UpdateCorrelationRulesetIfUnchanged(*storage.CorrelationRuleset, time.Time) (bool, error)
}

type testRunnerCorrelationRulesetService struct {
	runner *TestRunner
	store  storage.Store
}

func newTestRunnerCorrelationRulesetService(runner *TestRunner, store storage.Store) correlationRulesetService {
	return &testRunnerCorrelationRulesetService{runner: runner, store: store}
}

func (service *testRunnerCorrelationRulesetService) StorageConfigured() bool {
	return service != nil && service.store != nil
}

func (service *testRunnerCorrelationRulesetService) GetRuleset(id string) (*storage.CorrelationRuleset, error) {
	return service.store.GetCorrelationRuleset(id)
}

func (service *testRunnerCorrelationRulesetService) SaveRuleset(ruleset *storage.CorrelationRuleset) error {
	return service.store.SaveCorrelationRuleset(ruleset)
}

func (service *testRunnerCorrelationRulesetService) ListRulesets(projectID string, limit, offset int) ([]*storage.CorrelationRuleset, error) {
	return service.store.ListCorrelationRulesets(projectID, limit, offset)
}

func (service *testRunnerCorrelationRulesetService) UpdateRulesetIfUnchanged(ruleset *storage.CorrelationRuleset, expectedUpdatedAt time.Time) (bool, error) {
	store, ok := service.store.(correlationRulesetCASPersistence)
	if !ok {
		return false, errCorrelationRulesetOptimisticUpdateUnsupported
	}
	return store.UpdateCorrelationRulesetIfUnchanged(ruleset, expectedUpdatedAt)
}

func (service *testRunnerCorrelationRulesetService) DeleteRulesetIfUnchanged(ruleset *storage.CorrelationRuleset, expectedProjectID string) (bool, error) {
	if ruleset == nil {
		return false, nil
	}
	store, ok := service.store.(interface {
		DeleteCorrelationRulesetIfUnchanged(string, string, time.Time) (bool, error)
	})
	if !ok {
		return false, errCorrelationRulesetGuardedDeleteUnsupported
	}
	return store.DeleteCorrelationRulesetIfUnchanged(ruleset.ID, expectedProjectID, ruleset.UpdatedAt)
}

func (service *testRunnerCorrelationRulesetService) GetScript(id string) (*StoredScript, bool) {
	return service.runner.GetScript(id)
}

func (service *testRunnerCorrelationRulesetService) GetScriptRecording(id string) (*storage.ScriptRecordingRecord, error) {
	return service.store.GetScriptRecording(id)
}

func (service *testRunnerCorrelationRulesetService) UpdateScriptIfUnchangedAndRulesetCurrent(
	id string,
	script *dsl.Script,
	projectID string,
	expectedUpdatedAt time.Time,
	expectedActionGraphDigest string,
	rulesetID string,
	expectedRulesetUpdatedAt time.Time,
) (bool, error) {
	_, updated, err := service.runner.UpdateScriptDurableInProjectIfUnchangedAndCorrelationRulesetCurrent(
		id,
		script,
		projectID,
		expectedUpdatedAt,
		expectedActionGraphDigest,
		rulesetID,
		expectedRulesetUpdatedAt,
	)
	return updated, err
}

func (s *Server) objectRepositoryDomainService() objectRepositoryService {
	if s.objectRepositoryService != nil {
		if adapter, ok := s.objectRepositoryService.(*testRunnerObjectRepositoryService); ok && (adapter.runner != s.runner || !sameStorageStore(adapter.store, s.store)) {
			return newTestRunnerObjectRepositoryService(s.runner, s.store)
		}
		return s.objectRepositoryService
	}
	return newTestRunnerObjectRepositoryService(s.runner, s.store)
}

func (s *Server) correlationRulesetDomainService() correlationRulesetService {
	if s.correlationRulesetService != nil {
		if adapter, ok := s.correlationRulesetService.(*testRunnerCorrelationRulesetService); ok && (adapter.runner != s.runner || !sameStorageStore(adapter.store, s.store)) {
			return newTestRunnerCorrelationRulesetService(s.runner, s.store)
		}
		return s.correlationRulesetService
	}
	return newTestRunnerCorrelationRulesetService(s.runner, s.store)
}

// sameStorageStore compares compatibility-adapter stores without assuming that
// every possible storage.Store implementation has a comparable dynamic value.
// Non-comparable implementations conservatively rebuild the lightweight
// adapter so Server literals and focused tests always observe the current field.
func sameStorageStore(left, right storage.Store) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() || !leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}

var _ objectRepositoryService = (*testRunnerObjectRepositoryService)(nil)
var _ correlationRulesetService = (*testRunnerCorrelationRulesetService)(nil)
