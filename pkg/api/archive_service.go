package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jasonczech/apexload/pkg/storage"
)

// archiveService is the read-side boundary used by archive HTTP handlers. The
// compatibility adapter keeps the existing TestRunner behavior while preventing
// handlers from reaching through the Server composition root for archive state.
// Retention mutation/fencing remains on Server until that state machine can move
// as one atomic slice.
type archiveService interface {
	GetTest(id string) (*ManagedTest, bool)
	GetTestManifest(id string) (*TestRunManifest, error)
	GetRunArchive(id string) (*storage.RunArchiveRecord, bool)
	GetRunArchiveContext(ctx context.Context, id string) (*storage.RunArchiveRecord, bool)
	GetRunArchiveManifest(id string) (*RunArchiveManifest, error)
	RetentionInputs() (ArchiveRetentionSettings, []*ManagedTest)
	GetRetentionArchive(id string) (*storage.RunArchiveRecord, bool, error)
}

type testRunnerArchiveService struct {
	runner *TestRunner
	store  storage.Store
}

func newTestRunnerArchiveService(runner *TestRunner, store storage.Store) archiveService {
	return &testRunnerArchiveService{runner: runner, store: store}
}

func (service *testRunnerArchiveService) GetTest(id string) (*ManagedTest, bool) {
	if service == nil || service.runner == nil {
		return nil, false
	}
	return service.runner.GetTest(id)
}

func (service *testRunnerArchiveService) GetTestManifest(id string) (*TestRunManifest, error) {
	if service == nil || service.runner == nil {
		return nil, fmt.Errorf("test runner is unavailable")
	}
	return service.runner.GetTestManifest(id)
}

func (service *testRunnerArchiveService) GetRunArchive(id string) (*storage.RunArchiveRecord, bool) {
	if service == nil || service.runner == nil {
		return nil, false
	}
	return service.runner.GetRunArchive(id)
}

func (service *testRunnerArchiveService) GetRunArchiveContext(ctx context.Context, id string) (*storage.RunArchiveRecord, bool) {
	if service == nil || service.runner == nil {
		return nil, false
	}
	return service.runner.GetRunArchiveContext(ctx, id)
}

func (service *testRunnerArchiveService) GetRunArchiveManifest(id string) (*RunArchiveManifest, error) {
	if service == nil || service.runner == nil {
		return nil, fmt.Errorf("test runner is unavailable")
	}
	return service.runner.GetRunArchiveManifest(id)
}

func (service *testRunnerArchiveService) RetentionInputs() (ArchiveRetentionSettings, []*ManagedTest) {
	if service == nil || service.runner == nil {
		return ArchiveRetentionSettings{}, nil
	}
	return service.runner.GetSystemSettings().ArchiveRetention, service.runner.listOperationalTestSnapshots()
}

// GetRetentionArchive preserves the retention handler's historical fallback to
// durable metadata when the live archive accessor cannot materialize an object.
func (service *testRunnerArchiveService) GetRetentionArchive(id string) (*storage.RunArchiveRecord, bool, error) {
	if record, ok := service.GetRunArchive(id); ok {
		return record, true, nil
	}
	if service == nil || service.store == nil {
		return nil, false, nil
	}
	record, err := service.store.GetRunArchive(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if record == nil {
		return nil, false, nil
	}
	return record, true, nil
}

func (s *Server) archiveDomainService() archiveService {
	if s.archiveService != nil {
		if compatibility, ok := s.archiveService.(*testRunnerArchiveService); ok && compatibility.runner != s.runner {
			return newTestRunnerArchiveService(s.runner, s.store)
		}
		return s.archiveService
	}
	// Compatibility for tests and internal callers that still construct Server
	// values directly instead of using NewServer.
	return newTestRunnerArchiveService(s.runner, s.store)
}

var _ archiveService = (*testRunnerArchiveService)(nil)
